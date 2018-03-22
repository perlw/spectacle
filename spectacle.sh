#!/bin/sh
go get -u golang.org/x/vgo &> goget.log
$GOPATH/bin/vgo build -o bin/spectacle &> build.log

pkill spectacle
cp bin/spectacle /home/perlw/services/
cd /home/perlw/services
nohup ./spectacle &> /var/log/spectacle.log &
