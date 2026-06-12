#!/usr/bin/bash

runTestsAndBenchs() (
  local directory=$1
  echo "Running benchmarks at: $directory"

  cd $directory
  go test -v -bench . -run . -benchmem ./...
)



runTestsAndBenchs ./
runTestsAndBenchs ./bench/bleve
runTestsAndBenchs ./bench/bluge_upstream
runTestsAndBenchs ./bench/bluge_fork
