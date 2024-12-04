#!/bin/bash

# MIT License
#
# Copyright (c) 2023 Matheus Pimenta
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

function license() {
    local comment="$1"
    local fn="$2"

    cat << EOF > $fn.new
${comment} MIT License
${comment}
${comment} Copyright (c) $(date +"%Y") Matheus Pimenta
${comment}
${comment} Permission is hereby granted, free of charge, to any person obtaining a copy
${comment} of this software and associated documentation files (the "Software"), to deal
${comment} in the Software without restriction, including without limitation the rights
${comment} to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
${comment} copies of the Software, and to permit persons to whom the Software is
${comment} furnished to do so, subject to the following conditions:
${comment}
${comment} The above copyright notice and this permission notice shall be included in all
${comment} copies or substantial portions of the Software.
${comment}
${comment} THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
${comment} IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
${comment} FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
${comment} AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
${comment} LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
${comment} OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
${comment} SOFTWARE.

EOF

    cat $fn >> $fn.new
    mv $fn.new $fn
}

function license_pattern() {
    local comment="$1"
    local pattern="$2"

    for f in `find . -wholename "$pattern"`; do
        if ! grep -q "MIT License" $f; then
            license "$comment" "$f"
        fi
    done
}

# files with double-slash comments
for pattern in \*.go \*/templates/\*.cue \*k8s/\*.cue; do
    license_pattern "//" "$pattern"
done

# files with hashtag comments
for pattern in \*.yaml \*.yml \*.tf ./Dockerfile ./Dockerfile.test ./Makefile; do
    license_pattern "#" "$pattern"
done
