#!/bin/bash
for (( ; ; ))
do
	cat access.log >> access2.log
	sleep 1
done
