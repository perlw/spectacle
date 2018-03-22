#!/bin/sh
go get -u golang.org/x/vgo &> goget.log
$GOPATH/bin/vgo build -o bin/spectacle &> build.log

pkill spectacle
echo $HOME &> home.log
cp bin/spectacle $HOME/services/
cd $HOME/services
nohup ./spectacle &> /var/log/spectacle.log &
