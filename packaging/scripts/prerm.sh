#!/bin/bash
if [ "$1" = "remove" ]; then
    systemctl stop titan-web-server.service || :
    systemctl disable titan-web-server.service || :
    systemctl daemon-reload
fi
