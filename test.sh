#!/bin/bash
set -euo pipefail

(cd code && go test -race ./...)
(cd frontend && CI=true yarn test --watchAll=false --passWithNoTests)
