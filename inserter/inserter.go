package main

import (
	"database/sql"
	"flag"
	"fmt"
	"github.com/Shopify/sarama"
	cluster "github.com/bsm/sarama-cluster"
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
	"time"
)

var (
	LogLevel = flag.String("loglevel", "info", "Log level")

	MetricsAddr = flag.String("metrics.addr", ":8081", "Metrics address")
	MetricsPath = flag.String("metrics.path", "/metrics", "Metrics path")

	KafkaTopic = flag.String("kafka.topic", "flows-processed", "Kafka topic to consume from")
	KafkaBrk   = flag.String("kafka.brokers", "127.0.0.1:9092,[::1]:9092", "Kafka brokers list separated by commas")
	KafkaGroup = flag.String("kafka.group", "postgres-inserter", "Kafka group id")
	FlushTime  = flag.String("flush.dur", "5s", "Flush duration")
	FlushCount = flag.Int("flush.count", 100, "Flush count")

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
	msgCount int
	last     time.Time
	dur      time.Duration

	db *sql.DB

	flows    [][]interface{}
	offstash *cluster.OffsetStash
	consumer *cluster.Consumer
}

func (s *state) flush() bool {
	log.Infof("Processed %d records in the last iteration.", s.msgCount)
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

	s.consumer.MarkOffsets(s.offstash)
	s.offstash = cluster.NewOffsetStash()
	s.flows = make([][]interface{}, 0)
	return true
}

func (s *state) buffer(msg *sarama.ConsumerMessage, cur time.Time) (bool, error, time.Time) {
	s.msgCount++

	var fmsg flow.FlowMessage

	err := proto.Unmarshal(msg.Value, &fmsg)
	if err != nil {
		log.Printf("unmarshaling error: ", err)
	} else {
		log.Debug(fmsg)
		ts := time.Unix(int64(fmsg.TimeFlow), 0)

		srcip := net.IP(fmsg.SrcIP)
		dstip := net.IP(fmsg.DstIP)
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
	s.offstash.MarkOffset(msg, "")

	return false, nil, cur
}

func closeAll(db *sql.DB, consumer *cluster.Consumer) {
	consumer.Close()
	db.Close()
}

func main() {
	flag.Parse()

	lvl, _ := log.ParseLevel(*LogLevel)
	log.SetLevel(lvl)

	s := &state{
		last:     time.Time{},
		offstash: cluster.NewOffsetStash(),
	}
	go s.metricsHTTP()

	config := cluster.NewConfig()
	brokers := strings.Split(*KafkaBrk, ",")
	topics := []string{*KafkaTopic}
	consumer, err := cluster.NewConsumer(brokers, *KafkaGroup, topics, config)
	if err != nil {
		log.Fatal(err)
	}
	s.consumer = consumer

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
	defer closeAll(db, consumer)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	durFlush, _ := time.ParseDuration(*FlushTime)
	var count int
	timer := time.After(durFlush)
	for {
		select {
		case <-timer:
			s.flush()
			timer = time.After(durFlush)
		case msg, ok := <-consumer.Messages():
			if ok {
				log.Debugf("%s/%d/%d\t%s\t", msg.Topic, msg.Partition, msg.Offset, msg.Key)
				flush, err, _ := s.buffer(msg, time.Now().UTC())
				if flush {
					s.flush()
				}
				if err != nil {
					log.Errorf("Error while processing: %v", err)
				}
				count++
				if count == *FlushCount {
					s.flush()
					count = 0
				}
			}
		case <-signals:
			return
		}
	}
	log.Info("Stopped processing")
}
