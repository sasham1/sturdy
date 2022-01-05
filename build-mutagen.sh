#!/bin/bash

set -euo pipefail

dir=$(pwd)
mutagen_repo_dir="/tmp/mutagen"

build_agent() {
  local BRANCH=$1
  local EXPECTED_COMMIT=$2
  local VERSION=$3

  echo "⭐ [build_agent] $@"

  pushd ${mutagen_repo_dir}

  local HEAD_COMMIT=$(git rev-parse origin/$BRANCH)
  if [ "$HEAD_COMMIT" != "$EXPECTED_COMMIT" ]; then
    echo "Unexpected commit on origin/$BRANCH: expected ${EXPECTED_COMMIT} got ${HEAD_COMMIT}"
    exit 1
  fi

  git checkout origin/$BRANCH

  GOOS=linux GOARCH=amd64 go build \
    -v \
    -ldflags "-X github.com/mutagen-io/mutagen/pkg/sturdy/api.clientVersion=mutagen-agent/$VERSION" \
    -o "${dir}/mutagen-agent-$VERSION" \
    github.com/mutagen-io/mutagen/cmd/mutagen-agent
  popd
}

build_agent "sturdy" "18fa4aac554f34841dc34bfaae5bbbde46ffad05" "v0.12.0-beta2"
build_agent "sturdy-v0.12" "ff3b34ad09689b2af268ec69dcf17369d68f09b7" "v0.12.0-beta6"
build_agent "sturdy-v0.12.0-beta7" "0a9d7522332600e273b32538244a77554b893b50" "v0.12.0-beta7" # Sturdy v0.7.0 and
build_agent "sturdy-v0.13.0-beta2" "7ab32f9ea468769666a73fb059114a44becc3cc2" "v0.13.0-beta2"
