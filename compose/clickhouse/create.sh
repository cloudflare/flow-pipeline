#!/bin/bash
set -e

clickhouse client -n <<-EOSQL
    CREATE TABLE IF NOT EXISTS flows
    (
        TimeReceived UInt64,
        TimeFlowStart UInt64,

        SequenceNum UInt32,
        SamplingRate UInt64,
        SamplerAddress FixedString(16),

        SrcAddr FixedString(16),
        DstAddr FixedString(16),

        SrcAS UInt32,
        DstAS UInt32,

        EType UInt32,
        Proto UInt32,

        SrcPort UInt32,
        DstPort UInt32,

        InIf UInt32,

        Bytes UInt64,
        Packets UInt64
    ) ENGINE = Kafka()
    SETTINGS
        kafka_broker_list = 'kafka:9092',
        kafka_topic_list = 'flows',
        kafka_group_name = 'clickhouse',
        kafka_format = 'Protobuf',
        kafka_schema = 'flow.proto:FlowMessage';

    CREATE TABLE IF NOT EXISTS flows_raw
    (
        Date Date,
        TimeReceived DateTime,
        TimeFlowStart DateTime,

        SequenceNum UInt32,
        SamplingRate UInt64,
        SamplerAddress FixedString(16),

        SrcAddr FixedString(16),
        DstAddr FixedString(16),

        SrcAS UInt32,
        DstAS UInt32,

        EType UInt32,
        Proto UInt32,

        SrcPort UInt32,
        DstPort UInt32,

        InIf UInt32,

        Bytes UInt64,
        Packets UInt64
    ) ENGINE = MergeTree()
    PARTITION BY Date
    ORDER BY TimeReceived;

    CREATE MATERIALIZED VIEW IF NOT EXISTS flows_raw_view TO flows_raw
    AS SELECT
        toDate(TimeReceived) AS Date,
        *
       FROM flows;

    CREATE TABLE IF NOT EXISTS flows_5m
    (
        Date Date,
        Timeslot DateTime,

        SamplerAddress FixedString(16),
        InIf UInt32,

        -- SrcAS UInt32,
        -- DstAS UInt32,

        ETypeMap Nested (
            EType UInt32,
            Bytes UInt64,
            Packets UInt64,
            Count UInt64
        ),

        Bytes UInt64,
        Packets UInt64,
        Count UInt64
    ) ENGINE = SummingMergeTree()
    PARTITION BY Date
    ORDER BY (Date, Timeslot, SamplerAddress, InIf, \`ETypeMap.EType\`);

    CREATE MATERIALIZED VIEW IF NOT EXISTS flows_5m_view TO flows_5m
    AS
        SELECT
            Date,
            toStartOfFiveMinute(TimeReceived) AS Timeslot,
            SamplerAddress,
            InIf,

            [EType] AS \`ETypeMap.EType\`,
            [Bytes] AS \`ETypeMap.Bytes\`,
            [Packets] AS \`ETypeMap.Packets\`,
            [Count] AS \`ETypeMap.Count\`,

            sum(Bytes) AS Bytes,
            sum(Packets) AS Packets,
            count() AS Count

        FROM flows_raw
        GROUP BY Date, Timeslot, SamplerAddress, InIf, \`ETypeMap.EType\`;
    CREATE TABLE IF NOT EXISTS Event_queue
    (
      message String
    ) ENGINE = Kafka()
      SETTINGS
          kafka_broker_list = 'kafka:9092',
          kafka_topic_list = 'events',
          kafka_group_name = 'ndpid',
          kafka_format = 'JSONAsString',
          kafka_num_consumers = 5;
    CREATE TABLE IF NOT EXISTS Flow
    (
        source String,
        alias String,
        flow_event_name String,
        flow_id UInt64,
        flow_state String,
        flow_src_packets_processed UInt64,
        flow_dst_packets_processed UInt64,
        flow_first_seen DateTime,
        flow_src_last_pkt_time DateTime,
        flow_dst_last_pkt_time DateTime,
        l3_proto String,
        l4_proto String,
        src_ip String,
        dst_ip String,
        src_port UInt32,
        dst_port UInt32,
        ndpi_proto String,
        ndpi_proto_ip String,
        ndpi_category String,
        ndpi_breed String
    ) ENGINE = MergeTree()
      ORDER BY (flow_id);
    CREATE MATERIALIZED VIEW IF NOT EXISTS Flow_view to Flow
    AS SELECT
        JSONExtractString(message, 'source') AS source,
        JSONExtractString(message, 'alias') AS alias,
        JSONExtractString(message, 'flow_event_name') AS flow_event_name,
        JSONExtract(message, 'flow_id', 'UInt64') AS flow_id,
        JSONExtractString(message, 'flow_state') AS flow_state,
        JSONExtract(message, 'flow_src_packets_processed', 'UInt16') AS flow_src_packets_processed,
        JSONExtract(message, 'flow_dst_packets_processed', 'UInt16') AS flow_dst_packets_processed,
        FROM_UNIXTIME(intDiv(JSONExtract(message, 'flow_first_seen', 'UInt64'),1000000)) AS flow_first_seen,
        FROM_UNIXTIME(intDiv(JSONExtract(message, 'flow_src_last_pkt_time', 'UInt64'),1000000)) AS flow_src_last_pkt_time,
        FROM_UNIXTIME(intDiv(JSONExtract(message, 'flow_dst_last_pkt_time', 'UInt64'),1000000)) AS flow_dst_last_pkt_time,
        JSONExtractString(message, 'l3_proto') AS l3_proto,
        JSONExtractString(message, 'l4_proto') AS l4_proto,
        JSONExtractString(message, 'src_ip') AS src_ip,
        JSONExtractString(message, 'dst_ip') AS dst_ip,
        JSONExtract(message, 'src_port', 'UInt32') AS src_port,
        JSONExtract(message, 'dst_port', 'UInt32') AS dst_port,
        JSONExtractString(message, 'ndpi', 'proto') AS ndpi_proto,
        JSONExtractString(message, 'ndpi', 'proto_by_ip') AS ndpi_proto_ip,
        JSONExtractString(message, 'ndpi', 'category') AS ndpi_category,
        JSONExtractString(message, 'ndpi', 'breed') AS ndpi_breed
    FROM Event_queue
    WHERE notEmpty(flow_event_name);
    CREATE TABLE IF NOT EXISTS dnsflow
    (
        flow_first_seen DateTime,
        src_ip String,
        dst_ip String,
        src_port UInt32,
        dst_port UInt32,
        ndpi_proto String,
        ndpi_proto_ip String,
        num_queries Int,
        num_answers Int,
        reply_code Int,
        query_type Int,
        rsp_type Int
    ) ENGINE = MergeTree()
      ORDER BY (flow_first_seen);
    CREATE MATERIALIZED VIEW IF NOT EXISTS dnsflow_view to dnsflow
    AS SELECT
        FROM_UNIXTIME(intDiv(JSONExtract(message, 'flow_first_seen', 'UInt64'),1000000)) AS flow_first_seen,
        JSONExtractString(message, 'src_ip') AS src_ip,
        JSONExtractString(message, 'dst_ip') AS dst_ip,
        JSONExtract(message, 'src_port', 'UInt32') AS src_port,
        JSONExtract(message, 'dst_port', 'UInt32') AS dst_port,
        JSONExtractString(message, 'ndpi', 'proto') AS ndpi_proto,
        JSONExtractString(message, 'ndpi', 'proto_by_ip') AS ndpi_proto_ip,
        JSONExtractInt(message, 'ndpi', 'dns', 'num_queries') AS num_queries,
        JSONExtractInt(message, 'ndpi', 'dns', 'num_answers') AS num_answers,
        JSONExtractInt(message, 'ndpi', 'dns', 'reply_code') AS reply_code,
        JSONExtractInt(message, 'ndpi', 'dns', 'query_type') AS query_type,
        JSONExtractInt(message, 'ndpi', 'dns', 'rsp_type') AS rsp_type
    FROM Event_queue
    WHERE ndpi_proto LIKE 'DNS%';
    CREATE TABLE IF NOT EXISTS snmpflow
    (
        flow_first_seen DateTime,
        src_ip String,
        dst_ip String,
        src_port UInt32,
        dst_port UInt32,
        ndpi_proto String,
        ndpi_proto_ip String,
        version Int,
        primitive Int
    ) ENGINE = MergeTree()
      ORDER BY (flow_first_seen);
    CREATE MATERIALIZED VIEW IF NOT EXISTS snmpflow_view to snmpflow
    AS SELECT
        FROM_UNIXTIME(intDiv(JSONExtract(message, 'flow_first_seen', 'UInt64'),1000000)) AS flow_first_seen,
        JSONExtractString(message, 'src_ip') AS src_ip,
        JSONExtractString(message, 'dst_ip') AS dst_ip,
        JSONExtract(message, 'src_port', 'UInt32') AS src_port,
        JSONExtract(message, 'dst_port', 'UInt32') AS dst_port,
        JSONExtractString(message, 'ndpi', 'proto') AS ndpi_proto,
        JSONExtractString(message, 'ndpi', 'proto_by_ip') AS ndpi_proto_ip,
        JSONExtractInt(message, 'ndpi', 'snmp', 'version') AS version,
        JSONExtractInt(message, 'ndpi', 'snmp', 'primitive') AS primitive
    FROM Event_queue
    WHERE ndpi_proto LIKE 'SNMP%';
    CREATE TABLE IF NOT EXISTS ntpflow
    (
        flow_first_seen DateTime,
        src_ip String,
        dst_ip String,
        src_port UInt32,
        dst_port UInt32,
        ndpi_proto String,
        ndpi_proto_ip String,
        request_code Int,
        version Int
    ) ENGINE = MergeTree()
      ORDER BY (flow_first_seen);
    CREATE MATERIALIZED VIEW IF NOT EXISTS ntpflow_view to ntpflow
    AS SELECT
        FROM_UNIXTIME(intDiv(JSONExtract(message, 'flow_first_seen', 'UInt64'),1000000)) AS flow_first_seen,
        JSONExtractString(message, 'src_ip') AS src_ip,
        JSONExtractString(message, 'dst_ip') AS dst_ip,
        JSONExtract(message, 'src_port', 'UInt32') AS src_port,
        JSONExtract(message, 'dst_port', 'UInt32') AS dst_port,
        JSONExtractString(message, 'ndpi', 'proto') AS ndpi_proto,
        JSONExtractString(message, 'ndpi', 'proto_by_ip') AS ndpi_proto_ip,
        JSONExtractInt(message, 'ndpi', 'ntp', 'request_code') AS request_code,
        JSONExtractInt(message, 'ndpi', 'ntp', 'version') AS version
    FROM Event_queue
    WHERE ndpi_proto LIKE 'NTP%';
EOSQL
