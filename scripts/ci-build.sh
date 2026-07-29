#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# A caller may provide an alternate checked-out/shared config. If it is absent,
# the repository config is the source of truth. Missing both means safe defaults.
CONFIG_CANDIDATE=${BUILD_CONFIG_FILE:-${APP_BUILD_CONFIG:-}}
if [[ -n "$CONFIG_CANDIDATE" && -f "$CONFIG_CANDIDATE" ]]; then
  CONFIG_FILE=$CONFIG_CANDIDATE
elif [[ -f "$ROOT_DIR/.github/build-config.json" ]]; then
  CONFIG_FILE=$ROOT_DIR/.github/build-config.json
else
  CONFIG_FILE=""
fi

if [[ -n "$CONFIG_FILE" ]]; then
  echo "build config: $CONFIG_FILE"
  CONFIG_JSON=$(cat "$CONFIG_FILE")
else
  echo "build config: built-in fallback"
  CONFIG_JSON='{"app":"app","module":"github.com/o25160526-pip/go-selfupdate-template","targets":[{"goos":"linux","goarch":"amd64","extension":""}],"checks":{"run_vet":true,"run_tests":true,"race":true,"verify_version":true,"verify_artifacts":true}}'
fi

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
APP=$(jq -er '.app // "app"' <<<"$CONFIG_JSON")
MODULE=$(jq -er '.module // "github.com/o25160526-pip/go-selfupdate-template"' <<<"$CONFIG_JSON")
RUN_VET=$(jq -r '.checks.run_vet // true' <<<"$CONFIG_JSON")
RUN_TESTS=$(jq -r '.checks.run_tests // true' <<<"$CONFIG_JSON")
RACE=$(jq -r '.checks.race // true' <<<"$CONFIG_JSON")
VERIFY_VERSION=$(jq -r '.checks.verify_version // true' <<<"$CONFIG_JSON")
VERIFY_ARTIFACTS=$(jq -r '.checks.verify_artifacts // true' <<<"$CONFIG_JSON")

if [[ "${BUILD_ONLY:-false}" != "true" ]]; then
  if [[ "$RUN_VET" == "true" ]]; then go vet ./...; fi
  if [[ "$RUN_TESTS" == "true" ]]; then
    if [[ "$RACE" == "true" ]]; then go test ./... -race -count=1; else go test ./... -count=1; fi
  fi
fi

VERSION=${VERSION:-$(go run ./tools/genversion -check-tags=false -format=display)}
COMMIT=${GITHUB_SHA:-${BUILD_SOURCEVERSION:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}}
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
DIST_DIR=${DIST_DIR:-dist}
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

if [[ "$VERIFY_VERSION" == "true" ]]; then
  go run ./tools/genversion -check-tags=false -format=display >/dev/null
fi

TARGET_COUNT=$(jq -er '.targets | length' <<<"$CONFIG_JSON")
(( TARGET_COUNT > 0 )) || { echo "build config has no targets" >&2; exit 1; }
for i in $(seq 0 $((TARGET_COUNT - 1))); do
  GOOS=$(jq -er ".targets[$i].goos" <<<"$CONFIG_JSON")
  GOARCH=$(jq -er ".targets[$i].goarch" <<<"$CONFIG_JSON")
  EXT=$(jq -r ".targets[$i].extension // \"\"" <<<"$CONFIG_JSON")
  case "$GOOS/$GOARCH" in
    linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64) ;;
    *) echo "unsupported target $GOOS/$GOARCH" >&2; exit 1 ;;
  esac
  [[ "$GOOS" == "windows" && "$EXT" == ".exe" || "$GOOS" != "windows" && "$EXT" == "" ]] || { echo "invalid extension for $GOOS" >&2; exit 1; }
  out="$DIST_DIR/${APP}_${GOOS}_${GOARCH}${EXT}"
  echo "build: $out"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath \
    -ldflags "-s -w -X '${MODULE}/internal/version.Current=${VERSION}' -X '${MODULE}/internal/version.Commit=${COMMIT}' -X '${MODULE}/internal/version.BuildDate=${BUILD_DATE}'" \
    -o "$out" ./cmd/app
done

if [[ "$VERIFY_ARTIFACTS" == "true" ]]; then
  (cd "$DIST_DIR" && sha256sum "${APP}"_* > checksums.txt)
  jq -n --arg app "$APP" --arg module "$MODULE" --arg version "$VERSION" --arg commit "$COMMIT" --arg date "$BUILD_DATE" --arg config "$CONFIG_FILE" --argjson targets "$(jq '.targets' <<<"$CONFIG_JSON")" '{app:$app,module:$module,version:$version,commit:$commit,build_date:$date,config:$config,targets:$targets}' > "$DIST_DIR/build-manifest.json"
  expected=$TARGET_COUNT
  actual=$(find "$DIST_DIR" -maxdepth 1 -type f -name "${APP}_*" ! -name 'checksums.txt' | wc -l | tr -d ' ')
  [[ "$actual" == "$expected" ]] || { echo "artifact count $actual != $expected" >&2; exit 1; }
fi

echo "build complete: version=$VERSION targets=$TARGET_COUNT"
