#!/bin/sh

go build -o _test/pvr .

for file in _test/*.sh; do
  sh $file 
done