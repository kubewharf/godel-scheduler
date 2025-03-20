#!/bin/bash
docker run -d \
  --name etcd-server \
  -p 2479:2379 \
  -p 2480:2380 \
  quay.io/coreos/etcd:v3.5.9 \
  /usr/local/bin/etcd \
  --data-dir=/etcd-data \
  --name=etcd0 \
  --listen-client-urls=http://0.0.0.0:2479 \
  --advertise-client-urls=http://0.0.0.0:2479 \
  --listen-peer-urls=http://0.0.0.0:2480