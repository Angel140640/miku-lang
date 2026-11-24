#!/bin/sh
# Compila o Miku Lang para todas as plataformas (binários estáticos em dist/).
set -e
cd "$(dirname "$0")"
rm -rf dist && mkdir dist
for t in linux/arm64 linux/amd64 windows/amd64 darwin/arm64 darwin/amd64; do
  os=${t%/*}; arch=${t#*/}; ext=""
  [ "$os" = windows ] && ext=".exe"
  echo "→ $os/$arch"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w" -o "dist/miku-$os-$arch$ext" .
done
ls -la dist
