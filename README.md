# flow-pipeline

This repository contains a set of tools and examples for [GoFlow](https://github.com/cloudflare/goflow),
a NetFlow/IPFIX/sFlow collector by [Cloudflare](https://www.cloudflare.com).

## Start a flow pipeline

The demo directory contains a startup file for an example pipeline including:
* GoFlow: an sFlow collector
* A mock collector
* Kafka/Zookeeper
* A database (Postgres/clickhouse)

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


## Run a GoFlow insertion

If you want to send sFlow/NetFlow/IPFIX to a GoFlow, run the following:

Using Clickhouse (see next section):
```
$ cd compose
$ docker-compose -f docker-compose-clickhouse-collect.yml
```

Keep in mind this is a development/prototype setup.
Some components will likely not be able to process more than a few
thousands rows per second.
You will likely have to tweak configuration statements,
number of workers.

Using a production setup, GoFlow was able to process more than +100k flows
per seconds and insert them in a Clickhouse database.

## About the Clickhouse setup

If you choose to visualize in Grafana, you will need a
[Clickhouse Data source plugin](https://grafana.com/grafana/plugins/vertamedia-clickhouse-datasource).
You can connect to the compose Grafana which has the plugin installed.

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

**Regarding the storage of IP addresses:**
At the moment, the current Clickhouse table does not perform any transformation of the addresses before insertion.
The bytes are inserted in a `FixedString(16)` regardless of the family (IPv4, IPv6).
In the dashboards, the function `IPv6NumToString(SrcAddr)` is used.

For example, **192.168.1.1** will end up being **101:a8c0::**
```sql
WITH toFixedString(reinterpretAsString(ipv4), 16) AS ipv4c
SELECT
    '192.168.1.1' AS ip,
    IPv4StringToNum(ip) AS ipv4,
    IPv6NumToString(ipv4c) AS ipv6

┌─ip──────────┬───────ipv4─┬─ipv6───────┐
│ 192.168.1.1 │ 3232235777 │ 101:a8c0:: │
└─────────────┴────────────┴────────────┘
```

In order to convert it:
```sql
WITH IPv6StringToNum(ip) AS ipv6
SELECT
    '101:a8c0::' AS ip,
    reinterpretAsUInt32(ipv6) AS ipv6c,
    IPv4NumToString(ipv6c) AS ipv4

┌─ip─────────┬──────ipv6c─┬─ipv4────────┐
│ 101:a8c0:: │ 3232235777 │ 192.168.1.1 │
└────────────┴────────────┴─────────────┘
```

Which for instance to display either IPv4 or IPv6 in a single query:
```sql
SELECT
  if(EType = 0x800, IPv4NumToString(reinterpretAsUInt32(SrcAddr)), IPv6NumToString(SrcAddr) AS SrcIP
```

This will be fixed in future dashboard/db schema version.

