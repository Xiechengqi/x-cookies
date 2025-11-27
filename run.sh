#!/usr/bin/env bash

BASEPATH=`dirname $(readlink -f ${BASH_SOURCE[0]})` && cd $BASEPATH

name="redbook-scraper-chromium"
docker rm -f ${name}

## file port
# -p 5000:5000 \
## terminal port
# -p 2222:2222 \
## env
# -e NOVNC_PASSWORD="123123"
# -e TERMINAL_USER="admin"
# -e TERMINAL_PASSWORD="123123"

docker run -itd \
  --restart=always \
  --privileged \
  -p 22222:2222 \
  -p 27900:7900 \
  -p 28000:8000 \
  -p 28080:8080 \
  -v ${PWD}/user-data:/app/chromium/user-data \
  -v ${PWD}/main.py:/app/scripts/main.py \
  -v ${PWD}/twitter-scraper:/app/scripts/twitter-scraper \
  -e CHROMIUM_PROXY_SERVER=socks5://192.168.1.15:1080
  -e TERMINAL_RPOXY=socks5://192.168.1.15:1080 \
  -e LANG=C.UTF-8 \
  -e UV_DEFAULT_INDEX=https://mirrors.tuna.tsinghua.edu.cn/pypi/web/simple \
  -e CHROMIUM_CLEAN_SINGLETONLOCK=true \
  -e CHROMIUM_START_URLS="chrome://version,http://localhost:5000" \
  --name ${name} fullnode/remote-chromium-ubuntu:latest
