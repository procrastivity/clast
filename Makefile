.PHONY: test lint install uninstall release clean deps-check

test:
	./test/test-clast.sh

lint:
	shellcheck bin/clast lib/clast/**/*.bash test/*.sh

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
