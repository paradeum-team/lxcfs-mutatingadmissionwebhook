#!/bin/bash

ROOT=$(cd $(dirname $0)/../../; pwd)


export CA_BUNDLE=$(kubectl config view --raw --flatten -o json | jq -r '.clusters[] |  .cluster."certificate-authority-data"')

if command -v envsubst >/dev/null 2>&1; then
    envsubst
else
    sed -e "s|\${CA_BUNDLE}|${CA_BUNDLE}|g"
fi



