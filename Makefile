.PHONY: test lint nix-smoke npm-pack-check check-version-sync install install-local uninstall uninstall-local release clean deps-check

test:
	./test/test-clast.sh

lint:
	@files=$$(find lib/clast -type f -name '*.bash'; find test -maxdepth 1 -type f -name '*.sh'; [ -f bin/clast ] && echo bin/clast; [ -f bin/clast-plumbing ] && echo bin/clast-plumbing; [ -f hooks/snapshot.sh ] && echo hooks/snapshot.sh; [ -f install.sh ] && echo install.sh; [ -f uninstall.sh ] && echo uninstall.sh; [ -f contrib/nix-smoke.sh ] && echo contrib/nix-smoke.sh; [ -f contrib/npm-pack-check.sh ] && echo contrib/npm-pack-check.sh; [ -f contrib/check-version-sync.sh ] && echo contrib/check-version-sync.sh; [ -f contrib/migrate-slug.sh ] && echo contrib/migrate-slug.sh; [ -f contrib/release ] && echo contrib/release); \
	shellcheck -x $$files

nix-smoke:
	@if ! command -v nix >/dev/null 2>&1; then \
		echo "nix-smoke: skipping (nix not on PATH)" ; \
		exit 0 ; \
	fi
	./contrib/nix-smoke.sh

npm-pack-check:
	@if ! command -v npm >/dev/null 2>&1; then \
		echo "npm-pack-check: skipping (npm not on PATH)" ; \
		exit 0 ; \
	fi
	./contrib/npm-pack-check.sh

check-version-sync:
	./contrib/check-version-sync.sh

install:
	./install.sh

install-local:
	./install.sh ~/.local

uninstall:
	./uninstall.sh

uninstall-local:
	./uninstall.sh ~/.local

clean:
	rm -rf .test-tmp

release:
	./contrib/release

deps-check:
	@for tool in bash jq git shellcheck; do \
		if ! command -v $$tool >/dev/null 2>&1; then \
			echo "missing: $$tool" >&2; \
			exit 1; \
		fi; \
	done
	@echo "all required tools present"
