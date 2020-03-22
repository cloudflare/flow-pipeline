# flow-pipeline

This repository contains a set of tools and examples for [GoFlow](https://github.com/cloudflare/goflow),
a NetFlow/IPFIX/sFlow collector by [Cloudflare](https://www.cloudflare.com).

## Start a flow pipeline

The demo directory contains a startup file for an example pipeline including:
* GoFlow: an sFlow collector
* A mock collector
* Kafka/Zookeeper
* A database (Postgres/clickhouse)
* An inserter: to insert the flows in a database (for Postgres)

It will listen on port 6343/UDP for sFlow and 2055/UDP for NetFlow.

The protobuf provided in this repository is a light version of
the GoFlow original one. Only a handful of fields will be inserted.

A basic pipeline looks like this:

```



                   +------+         +-----+
     sFlow/NetFlow |goflow+--------->Kafka|
                   +------+         +-----+
                                       |
                                       +--------------+
                      Topic: flows     |              |
                                       |              |
                                 +-----v----+       +-v---------+
                                 | inserter |       |new service|
                                 +----------+       +-----------+
                                      |
                                      |
                                   +--v--+
                                   |  DB |
                                   +-----+

```

You can add a _processor_ that would enrich the data
by consuming from Kafka and re-injecting the data into Kafka 
or directly into the database.

For instance, IP addresses can be mapped to countries, ASN
or customer information.

A suggestion is extending the GoFlow protobuf with new fields.

## Run a mock insertion

A mock insertion replaces the GoFlow decoding part. A _mocker_ generates
protobuf messages and sends them to Kafka.

Clone the repository, then run the following (for Postgres):

```
$ cd compose
$ docker-compose -f docker-compose-postgres-mock.yaml
```

Wait a minute for all the components to start.

You can connect on the local Grafana http://localhost:3000 to look at the flows being collected.

## Run a GoFlow insertion

If you want to send sFlow/NetFlow/IPFIX to a GoFlow, run the following:

Using Postgres:
```
$ cd compose
$ docker-compose -f docker-compose-postgres-collect.yaml
```

Using Clickhouse (see next section):
```
$ cd compose
$ docker-compose -f docker-compose-clickhouse-collect.yaml
```

## About the Clickhouse setup

The docker-compose does not embed Grafana or Prometheus.
If you choose to visualize in Grafana, you will need a
[Clickhouse Data source plugin](https://grafana.com/grafana/plugins/vertamedia-clickhouse-datasource).

The insertion is handled natively by Clickhouse:
* Creates a table with a [Kafka Engine](https://clickhouse.tech/docs/en/operations/table_engines/kafka/).
* Uses [Protobuf format](https://clickhouse.tech/docs/en/interfaces/formats/#protobuf).

Note: the protobuf messages to be written with their lengths.

Clickhouse will connect to Kafka periodically and fetch the content. Materialized views 
allow to store the data persistently and aggregate over fields.

To connect to the database, you have to run the following:
```
$ docker exec -ti compose_db_1 clickhouse-client
```

Once in the client CLI, a handful of tables are available:
* `flows` is directly connected to Kafka, it fetches from the current offset
* `flows_raw` contains the materialized view of `flows`
* `flows_5m` contains 5-minutes aggregates of ASN

Commands example:
```
:) DESCRIBE flows_raw

DESCRIBE TABLE flows_raw

┌─name───────────┬─type────────────┬─default_type─┬─default_expression─┬─comment─┬─codec_expression─┬─ttl_expression─┐
│ Date           │ Date            │              │                    │         │                  │                │
│ TimeReceived   │ DateTime        │              │                    │         │                  │                │
│ TimeFlowStart  │ DateTime        │              │                    │         │                  │                │
│ SequenceNum    │ UInt32          │              │                    │         │                  │                │
│ SamplingRate   │ UInt64          │              │                    │         │                  │                │
│ SamplerAddress │ FixedString(16) │              │                    │         │                  │                │
│ SrcAddr        │ FixedString(16) │              │                    │         │                  │                │
│ DstAddr        │ FixedString(16) │              │                    │         │                  │                │
│ SrcAS          │ UInt32          │              │                    │         │                  │                │
│ DstAS          │ UInt32          │              │                    │         │                  │                │
│ EType          │ UInt32          │              │                    │         │                  │                │
│ Proto          │ UInt32          │              │                    │         │                  │                │
│ SrcPort        │ UInt32          │              │                    │         │                  │                │
│ DstPort        │ UInt32          │              │                    │         │                  │                │
│ Bytes          │ UInt64          │              │                    │         │                  │                │
│ Packets        │ UInt64          │              │                    │         │                  │                │
└────────────────┴─────────────────┴──────────────┴────────────────────┴─────────┴──────────────────┴────────────────┘

:) SELECT Date,TimeReceived,IPv6NumToString(SrcAddr), IPv6NumToString(DstAddr), Bytes, Packets FROM flows_raw;

SELECT
    Date,
    TimeReceived,
    IPv6NumToString(SrcAddr),
    IPv6NumToString(DstAddr),
    Bytes,
    Packets
FROM flows_raw

┌───────Date─┬────────TimeReceived─┬─IPv6NumToString(SrcAddr)─┬─IPv6NumToString(DstAddr)─┬─Bytes─┬─Packets─┐
│ 2020-03-22 │ 2020-03-22 21:26:38 │ 2001:db8:0:1::80         │ 2001:db8:0:1::20         │   105 │      63 │
│ 2020-03-22 │ 2020-03-22 21:26:38 │ 2001:db8:0:1::c2         │ 2001:db8:0:1::           │   386 │      43 │
│ 2020-03-22 │ 2020-03-22 21:26:38 │ 2001:db8:0:1::6b         │ 2001:db8:0:1::9c         │   697 │      29 │
│ 2020-03-22 │ 2020-03-22 21:26:38 │ 2001:db8:0:1::81         │ 2001:db8:0:1::           │  1371 │      54 │
│ 2020-03-22 │ 2020-03-22 21:26:39 │ 2001:db8:0:1::87         │ 2001:db8:0:1::32         │   123 │      23 │

```

To look at aggregates (optimizing will run the summing operation).
The Nested structure allows to have sum per structures (in our case, per Ethernet-Type).

```
:) OPTIMIZE TABLE flows_5m;

OPTIMIZE TABLE flows_5m

Ok.

:) SELECT * FROM flows_5m WHERE SrcAS = 65001;

SELECT *
FROM flows_5m
WHERE SrcAS = 65001

┌───────Date─┬────────────Timeslot─┬─SrcAS─┬─DstAS─┬─ETypeMap.EType─┬─ETypeMap.Bytes─┬─ETypeMap.Packets─┬─ETypeMap.Count─┬─Bytes─┬─Packets─┬─Count─┐
│ 2020-03-22 │ 2020-03-22 21:25:00 │ 65001 │ 65000 │ [34525]        │ [2930]         │ [152]            │ [4]            │  2930 │     152 │     4 │
│ 2020-03-22 │ 2020-03-22 21:25:00 │ 65001 │ 65001 │ [34525]        │ [1935]         │ [190]            │ [3]            │  1935 │     190 │     3 │
│ 2020-03-22 │ 2020-03-22 21:25:00 │ 65001 │ 65002 │ [34525]        │ [4820]         │ [288]            │ [6]            │  4820 │     288 │     6 │
```

## Information and roadmap

This repository is an example and does not offer any warranties. I try to update it whenever I can.
Contributions are welcome.

The main purpose is for users to get started quickly and provide a basic system.
This should not be used in production.

I received requests to publish the Flink aggregator source code as you may have seen it
being used in GoFlow presentations.
Unfortunately, we moved entirely towards Clickhouse, the old code has not been updated in a while.
It may get published at some point but this is currently low priority.
