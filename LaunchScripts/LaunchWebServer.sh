#!/bin/bash

#export PATH=$PATH:/home/5k/.local/bin

gunicorn -w 1 -b 0.0.0.0:5000 MeshWebServer:app
