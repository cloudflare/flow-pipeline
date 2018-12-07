# flow-pipeline

This repository contains a set of tools and examples for [GoFlow](https://github.com/cloudflare/goflow),
a NetFlow/IPFIX/sFlow collector by [Cloudflare](https://www.cloudflare.com).

## Start a flow pipeline

The demo directory contains a startup file for an example pipeline including:
* GoFlow: an sFlow collector
* Kafka/Zookeeper
* A database
* An inserter: to insert the flows in a database

It will listen on port 6343/UDP for sFlow and 2055/UDP for NetFlow.

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

Clone the repository, then run the following:

```
cd compose
docker-compose -f docker-compose-mock.yaml
```

Wait a minute for all the components to start.

You can connect on the local Grafana http://localhost:3000 to look at the flows being collected.