#!/bin/bash
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    CREATE TABLE IF NOT EXISTS flows (
        id bigserial PRIMARY KEY,
        date_inserted timestamp default NULL,

        time_flow timestamp default NULL,
        type integer,
        sampling_rate integer,
        src_as bigint,
        dst_as bigint,
        src_ip inet,
        dst_ip inet,

        bytes bigint,
        packets bigint,

        etype integer,
        proto integer,
        src_port integer,
        dst_port integer
    );
EOSQL
