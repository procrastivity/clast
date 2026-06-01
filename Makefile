.PHONY: test lint install uninstall release clean deps-check

test:
	./test/test-clast.sh

lint:
	@files=$$(find lib/clast -type f -name '*.bash'; find test -maxdepth 1 -type f -name '*.sh'; [ -f bin/clast ] && echo bin/clast; [ -f hooks/snapshot.sh ] && echo hooks/snapshot.sh; [ -f install.sh ] && echo install.sh; [ -f uninstall.sh ] && echo uninstall.sh); \
	shellcheck -x $$files

install:
	./install.sh

uninstall:
	./uninstall.sh

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
