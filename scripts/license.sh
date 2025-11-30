#!/bin/bash

# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

function license() {
    local comment="$1"
    local fn="$2"

    cat << EOF > $fn.new
${comment} Copyright $(date +"%Y") Matheus Pimenta.
${comment} SPDX-License-Identifier: AGPL-3.0

EOF

    cat $fn >> $fn.new
    mv $fn.new $fn
}

function license_pattern() {
    local comment="$1"
    local pattern="$2"

    for f in `find . -wholename "$pattern"`; do
        if ! grep -q "SPDX-License-Identifier" $f; then
            license "$comment" "$f"
        fi
    done
}

# files with double-slash comments
for pattern in \*.go \*.c \*/templates/\*.cue \*testdata/\*.cue; do
    license_pattern "//" "$pattern"
done

# files with hashtag comments
for pattern in \*.yaml \*.yml \*.tf ./Dockerfile ./Dockerfile.test ./Makefile; do
    license_pattern "#" "$pattern"
done
