#!/usr/bin/env bash

docker run --rm --env-file ./env -p 8080:8080 dstait/spotify-playlist-maker:latest --auth
