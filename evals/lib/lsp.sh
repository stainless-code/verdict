#!/usr/bin/env bash
#
# LSP framing helpers for the `verdict lsp` cases. Messages are framed with
# `Content-Length` and split back by byte count, never by guessing at body
# text: no eval, no python, only printf, head, tail, and jq.
set -euo pipefail

# lsp_frame <json> prints one Content-Length framed message. LC_ALL=C makes
# ${#1} a byte count, which is what the header must carry.
lsp_frame() {
  local LC_ALL=C
  printf 'Content-Length: %d\r\n\r\n%s' "${#1}" "$1"
}

# lsp_bodies <raw-stream-file> prints each framed body on its own line, so the
# result is JSON Lines for `jq -s`. It fails when the stream holds anything but
# well-formed frames (a stray byte on stdout breaks the protocol channel).
lsp_bodies() {
  local file="$1" size offset=0 header len hbytes
  size="$(wc -c <"$file" | tr -d ' ')"
  while [ "$offset" -lt "$size" ]; do
    header="$(tail -c +"$((offset + 1))" "$file" | head -c 40 | head -n 1 | tr -d '\r')"
    case "$header" in
      "Content-Length: "[0-9]*) ;;
      *)
        echo "lsp_bodies: no frame header at byte $offset: $header" >&2
        return 1
        ;;
    esac
    len="${header#Content-Length: }"
    hbytes=$((${#header} + 4))
    tail -c +"$((offset + hbytes + 1))" "$file" | head -c "$len"
    printf '\n'
    offset=$((offset + hbytes + len))
  done
}

# lsp_file_uri <absolute-path> renders a file:// URI, escaping each segment.
lsp_file_uri() {
  jq -rn --arg p "$1" '"file://" + ($p | split("/") | map(@uri) | join("/"))'
}
