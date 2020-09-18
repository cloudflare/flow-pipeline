package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"github.com/Shopify/sarama"
	flow "github.com/cloudflare/flow-pipeline/pb-ext"
	proto "github.com/golang/protobuf/proto"
	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	LogLevel = flag.String("loglevel", "info", "Log level")

	MetricsAddr = flag.String("metrics.addr", ":8081", "Metrics address")
	MetricsPath = flag.String("metrics.path", "/metrics", "Metrics path")

	KafkaVersion = flag.String("kafka.version", "2.1.1", "Kafka version")
	KafkaTopic   = flag.String("kafka.topic", "flows-processed", "Kafka topic to consume from")
	KafkaBrk     = flag.String("kafka.brokers", "127.0.0.1:9092,[::1]:9092", "Kafka brokers list separated by commas")
	KafkaGroup   = flag.String("kafka.group", "postgres-inserter", "Kafka group id")
	FlushTime    = flag.Duration("flush.dur", time.Second*5, "Flush duration")
	FlushCount   = flag.Int("flush.count", 100, "Flush count")

	PostgresUser   = flag.String("postgres.user", "postgres", "Postgres user")
	PostgresPass   = flag.String("postgres.pass", "", "Postgres password")
	PostgresHost   = flag.String("postgres.host", "127.0.0.1", "Postgres host")
	PostgresPort   = flag.Int("postgres.port", 5432, "Postgres port")
	PostgresDbName = flag.String("postgres.dbname", "postgres", "Postgres database")

	Inserts = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "insert_count",
			Help: "Inserts made to Postgres.",
		},
	)

	flow_fields = []string{
		"date_inserted",
		"time_flow",
		"type",
		"sampling_rate",
		"src_ip",
		"dst_ip",
		"bytes",
		"packets",
		"src_port",
		"dst_port",
		"etype",
		"proto",
		"src_as",
		"dst_as",
	}
)

func (s *state) metricsHTTP() {
	prometheus.MustRegister(Inserts)
	http.Handle(*MetricsPath, promhttp.Handler())
	log.Fatal(http.ListenAndServe(*MetricsAddr, nil))
}

type state struct {
	ready chan bool

	msgCount int
	last     time.Time
	dur      time.Duration

	db *sql.DB

	lock  *sync.RWMutex
	flows [][]interface{}

	flushTimer <-chan time.Time
}

func (s *state) flush() bool {
	log.Infof("Processed %d records in the last iteration.", s.msgCount)
	s.lock.Lock()
	s.msgCount = 0

	flows_replace := make([]string, len(flow_fields))
	for i := range flow_fields {
		flows_replace[i] = fmt.Sprintf("$%v", i+1)
	}
	query := fmt.Sprintf("INSERT INTO flows (%v) VALUES (%v)", strings.Join(flow_fields, ", "), strings.Join(flows_replace, ", "))
	for _, curFlow := range s.flows {
		_, err := s.db.Exec(query, curFlow...)
		if err != nil {
			log.Debugf("TEST %v", curFlow)
			log.Fatal(err)
		}
	}

	s.flows = make([][]interface{}, 0)
	s.lock.Unlock()
	return true
}

func (s *state) buffer(msg *sarama.ConsumerMessage, cur time.Time) (bool, error, time.Time) {
	var flush bool
	s.lock.Lock()
	s.msgCount++

	if s.msgCount == *FlushCount {
		flush = true
	}

	var fmsg flow.FlowMessage

	err := proto.Unmarshal(msg.Value, &fmsg)
	if err != nil {
		log.Printf("unmarshalling error: ", err)
	} else {
		log.Debug(fmsg)
		ts := time.Unix(int64(fmsg.TimeFlowStart), 0)

		srcip := net.IP(fmsg.SrcAddr)
		dstip := net.IP(fmsg.DstAddr)
		srcipstr := srcip.String()
		dstipstr := dstip.String()
		if srcipstr == "<nil>" {
			srcipstr = "0.0.0.0"
		}
		if dstipstr == "<nil>" {
			dstipstr = "0.0.0.0"
		}

		extract := []interface{}{
			"NOW()",
			ts,
			fmsg.Type,
			fmsg.SamplingRate,
			srcipstr,
			dstipstr,
			fmsg.Bytes,
			fmsg.Packets,
			fmsg.SrcPort,
			fmsg.DstPort,
			fmsg.Etype,
			fmsg.Proto,
			fmsg.SrcAS,
			fmsg.DstAS,
		}
		s.flows = append(s.flows, extract)
	}
	s.lock.Unlock()
	if flush {
		s.flush()
	}
	return false, nil, cur
}

func (s *state) Setup(sarama.ConsumerGroupSession) error {
	close(s.ready)
	return nil
}

func (s *state) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

func (s *state) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for {
		select {
		case message := <-claim.Messages():
			log.Debugf("%s/%d/%d\t%s\t", message.Topic, message.Partition, message.Offset, message.Key)
			flush, err, _ := s.buffer(message, time.Now().UTC())
			if flush {
				s.flush()
			}
			if err != nil {
				log.Errorf("Error while processing: %v", err)
			}
			session.MarkMessage(message, "")
		case <-s.flushTimer:
			s.flush()
			s.flushTimer = time.After(*FlushTime)
		}
	}

	return nil
}

func main() {
	flag.Parse()

	lvl, _ := log.ParseLevel(*LogLevel)
	log.SetLevel(lvl)

	s := &state{
		last:       time.Time{},
		lock:       &sync.RWMutex{},
		flushTimer: time.After(*FlushTime),
		ready:      make(chan bool),
	}
	go s.metricsHTTP()

	kafkaVersion, err := sarama.ParseKafkaVersion(*KafkaVersion)
	if err != nil {
		log.Fatal(err)
	}

	config := sarama.NewConfig()
	config.Version = kafkaVersion

	pg_pass := *PostgresPass
	if pg_pass == "" {
		log.Debugf("Postgres password argument unset, using environment variable $POSTGRES_PASSWORD")
		pg_pass = os.Getenv("POSTGRES_PASSWORD")
	}

	info := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		*PostgresHost, *PostgresPort, *PostgresUser, pg_pass, *PostgresDbName)
	db, err := sql.Open("postgres", info)
	if err != nil {
		log.Fatal(err)
	}
	s.db = db

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())
	client, err := sarama.NewConsumerGroup(strings.Split(*KafkaBrk, ","), *KafkaGroup, config)
	if err != nil {
		log.Fatalf("Error creating consumer group client: %v", err)
	}

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			if err := client.Consume(ctx, strings.Split(*KafkaTopic, ","), s); err != nil {
				log.Fatalf("Error from consumer: %v", err)
			}
			if ctx.Err() != nil {
				return
			}
			s.ready = make(chan bool)
		}
	}()

	<-s.ready

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ctx.Done():
	case <-sigterm:
	}
	cancel()
	wg.Wait()
	if err = client.Close(); err != nil {
		log.Fatalf("Error closing client: %v", err)
	}
}
