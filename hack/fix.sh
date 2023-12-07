#!/bin/bash

args=-w;

while getopts "hd" arg; do
  case $arg in
    h)
      echo "usage: $0 [-d]";
      echo "  -d : dry run";
      exit 0;
      ;;
    d)
      args=-d;
      ;;
  esac
done

script_dir="$( cd -- "$(dirname "$0")" >/dev/null 2>&1; pwd -P )";
repo_root="${script_dir}/../";

pushd ${repo_root} > /dev/null;
for i in pkg test cmd;
do
  goimports ${args} ./${i}/;
  gofumpt ${args} ./${i}/;
done;
popd > /dev/null;
