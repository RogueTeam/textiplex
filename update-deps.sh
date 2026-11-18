#!/bin/sh

go get -u github.com/RoaringBitmap/roaring@latest
go get -u github.com/alecthomas/participle/v2@latest
go get -u github.com/stretchr/testify@latest
go get -u github.com/tidwall/btree/v2@latest
go get -u github.com/zeebo/xxh3@latest
go get -u golang.org/x/sys@latest

go mod tidy
