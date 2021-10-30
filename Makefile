VERSION := "v0.0.1"
BUILD_VERSION := $(VERSION)-$(shell git rev-parse --short HEAD)
DIRTY_LINES=$(shell git status --porcelain)

ifneq ($(DIRTY_LINES),)
   BUILD_VERSION := $(BUILD_VERSION)-dirty
endif

BRANCH=$(shell git rev-parse --abbrev-ref HEAD)
LDFLAGS_ARG="-X main.Version=$(BUILD_VERSION)"

default: clean compile
release: check-format default git-tag-push

clean:
	@rm -rf _dist

compile:
	@echo "compiling..."
	$(call compile_func,linux,amd64)
	$(call compile_func,darwin,amd64)
	$(call compile_func,darwin,arm64)
	@echo "done compiling."

format:
	gofmt -w -s .

check-format:
	@echo "checking files format..."
	@if [ -n "$$(gofmt -e -l .)" ]; then \
	  echo "error: unformatted go files."; \
	  echo "$$(gofmt -e -l -d .)"; \
	  echo 'To fix the formatting run `make format`.'; \
	  exit 1; \
	fi
	@echo "files formatted correctly."

git-tag-push:
	@echo "creating git tag $(VERSION)..."
	@if [ "$$(git rev-parse --abbrev-ref HEAD)" != "main" ]; then \
	  echo "error: cannot create a new tag because current branch is not main."; \
	  exit 1; \
	fi

	@if [ -n "$$(git status --porcelain)" ]; then \
	  echo "error: cannot create a git tag becauase there are uncommitted modifications."; \
	  echo "please ensure you have a clean working directory."; \
	  exit 1; \
	fi

	@git tag -a $(VERSION) -m "version $(VERSION)" HEAD
	@echo "pushing git tag $(VERSION)..."
	@git push origin $(VERSION)
	@echo "done."

define compile_func
	@echo "bgb-$1-$2..."
	@GOOS=$1 GOARCH=$2 go build -o _dist/bgb-$1-$2 -ldflags "-X main.Version=$(BUILD_VERSION)" .
endef
