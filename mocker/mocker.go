package main

import (
	"flag"
	"github.com/Shopify/sarama"
	flow "github.com/cloudflare/flow-pipeline/pb-ext"
	proto "github.com/golang/protobuf/proto"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
	"math/rand"
)

var (
	LogLevel = flag.String("loglevel", "info", "Log level")

	ProduceFreq = flag.Int("produce.freq", 100, "Produce interval in ms")
	ProduceRandom = flag.Int("produce.random", 300, "Add randomness")

	KafkaTopic = flag.String("kafka.topic", "flows", "Kafka topic to produce to")
	KafkaBrk   = flag.String("kafka.brokers", "127.0.0.1:9092,[::1]:9092", "Kafka brokers list separated by commas")
)

func main() {
	flag.Parse()

	lvl, _ := log.ParseLevel(*LogLevel)
	log.SetLevel(lvl)

	brokers := strings.Split(*KafkaBrk, ",")
	config := sarama.NewConfig()
	config.Producer.Return.Errors = true
	config.Producer.Return.Successes = false
	producer, err := sarama.NewAsyncProducer(brokers, config)
	log.Infof("Trying to connect to Kafka: %v", brokers)
	if err != nil {
		log.Fatal(err)
	}

	defer producer.Close()

	go func() {
		for {
			select {
			case err := <-producer.Errors():
				log.Error(err)
			}
		}
	}()

	var i uint32
	for {
		select {
		case <-time.After(time.Millisecond * time.Duration(*ProduceFreq + (rand.Int()%(*ProduceRandom)))):
			ts := time.Now().UTC().UnixNano()/1000000000

			bytes := rand.Int() % 1500
			packets := rand.Int() % 100
			srcas := rand.Int() % 3
			dstas := rand.Int() % 3

			srcip := []byte{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, }
			dstip := []byte{0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, }

			tmp := make([]byte, 1)
			rand.Read(tmp)
			srcip = append(srcip, tmp...)
			rand.Read(tmp)
			dstip = append(dstip, tmp...)

			srcport := rand.Int()
			dstport := rand.Int()

			fmsg := &flow.FlowMessage{
				SamplingRate: 1,
				Bytes: uint64(bytes),
				Packets: uint64(packets),
				SrcAS: uint32(65000+srcas),
				DstAS: uint32(65000+dstas),
				Etype: 0x86dd,
				SrcIP: srcip,
				DstIP: dstip,
				TimeFlow: uint64(ts),
				TimeRecvd: uint64(ts),
				SrcPort: uint32(srcport&0xFFFF),
				DstPort: uint32(dstport&0xFFFF),
				SequenceNum: i,
			}
			i++

			log.Debugf("Sending to %v: %v", *KafkaTopic, fmsg)

			b, _ := proto.Marshal(fmsg)
			producer.Input() <- &sarama.ProducerMessage{
				Topic: *KafkaTopic,
				Value: sarama.ByteEncoder(b),
			}
		}
	}

}
