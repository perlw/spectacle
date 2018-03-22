#!/bin/sh
vgo build -o bin/spectacle

pkill spectacle
cp bin/spectacle /home/perlw/services
cd /home/perlw/services
nohup ./spectacle &> /var/log/spectacle.log &
