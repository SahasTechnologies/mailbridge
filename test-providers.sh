#!/bin/bash
# test-providers.sh — builds MailBridge and runs its built-in `test` subcommand,
# which sends one test email through every configured provider using the
# exact same Go sender code MailBridge uses in production.
#
# Usage: run from your mailbridge directory (where main.go and .env live)
#   bash test-providers.sh [recipient@email.com]

set -uo pipefail

if [ ! -f main.go ]; then
  echo "ERROR: main.go not found in current directory. cd to your mailbridge folder first."
  exit 1
fi

echo "Building mailbridge..."
go build -o mailbridge . || { echo "Build failed."; exit 1; }

./mailbridge test "$@"
