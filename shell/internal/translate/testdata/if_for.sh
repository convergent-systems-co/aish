#!/usr/bin/env bash
NAME=world
if test -n "$NAME"; then
    echo "hello $NAME"
else
    echo "no name"
fi
for x in a b c; do
    echo $x
done
