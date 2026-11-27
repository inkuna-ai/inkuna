#!/bin/sh
# Regenerates raw.z, numstat.z, and all.patch in this directory: a commit
# with a rename with edits, a deletion, a mode-only change, a binary file, a
# file with no trailing newline, CRLF lines, and paths git writes raw
# (unicode, a space) or quotes. Run it from anywhere: sh make.sh
set -e
out=$(cd "$(dirname "$0")" && pwd)
repo=$(mktemp -d)
trap 'rm -rf "$repo"' EXIT
cd "$repo"
git init -q -b main
git config user.name fixture; git config user.email fixture@example.com; git config core.autocrlf false
mkdir -p src docs "docs/café"
printf 'package greet\n\nfunc Hello(name string) string {\n\treturn "Hello, " + name\n}\n\nfunc Bye(name string) string {\n\treturn "Bye, " + name\n}\n' > src/old_name.go
printf 'line one\nline two\nline three\n' > docs/remove.txt
printf '#!/bin/sh\necho run\n' > run.sh
printf 'first\nlast without newline' > tail.txt
printf 'alpha\r\nbeta\r\ngamma\r\n' > crlf.txt
printf 'naïve\n' > "docs/café/naïve.md"
printf '\000\001\002binary' > logo.bin
git add -A && git commit -qm base
git mv src/old_name.go src/new_name.go
printf 'package greet\n\nfunc Hello(name string) string {\n\treturn "Hello there, " + name\n}\n\nfunc Bye(name string) string {\n\treturn "Bye, " + name\n}\n' > src/new_name.go
git rm -q docs/remove.txt
chmod +x run.sh
printf 'first\nlast still without newline' > tail.txt
printf 'alpha\r\nBETA\r\ngamma\r\n' > crlf.txt
printf 'naïve and café\n' > "docs/café/naïve.md"
printf '\000\001\002binary2' > logo.bin
printf 'hello\n' > "docs/with space.txt"
printf 'quoted\n' > 'docs/say "hi".txt'
git add -A && git commit -qm change
g() { git -c core.quotePath=false -c diff.noprefix=false -c diff.mnemonicPrefix=false -c diff.relative=false -c diff.suppressBlankEmpty=false -c color.ui=false "$@"; }
g diff --raw -z -M -C --no-abbrev HEAD~1 HEAD > "$out/raw.z"
g diff --numstat -z -M -C HEAD~1 HEAD > "$out/numstat.z"
g diff -M -C -U3 --no-color --no-ext-diff --no-textconv --src-prefix=a/ --dst-prefix=b/ HEAD~1 HEAD > "$out/all.patch"
