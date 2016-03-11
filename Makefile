export GO15VENDOREXPERIMENT=1
PACKAGES=$(shell GO15VENDOREXPERIMENT=1 go list ./... | grep -v vendor)

all:
	cd -P . && \
	go build . && \
	go build -o ./minion/minion ./minion

install:
	cd -P . && go install .

deps:
	cd -P . && glide up --update-vendored

generate:
	go generate $(PACKAGES)

format:
	gofmt -w -s .

docker: build-linux
	docker build -t quay.io/netsys/di .
	cd -P minion && docker build -t quay.io/netsys/di-minion .
	cd -P di-tester && docker build -t quay.io/netsys/di-tester .

build-linux:
	export CGO_ENABLED=0 GOOS=linux GOARCH=amd64 && \
		    go build . && cd -P minion && go build .

check:
	go test $(PACKAGES)

lint: format
	cd -P . && go vet $(PACKAGES)
	for package in `echo $(PACKAGES) | grep -v minion/pb`; do \
		golint -min_confidence .25 $$package ; \
	done

coverage: db.cov dsl.cov engine.cov cluster.cov join.cov minion/supervisor.cov minion/network.cov minion.cov provider.cov

%.cov:
	go test -coverprofile=$@.out ./$*
	go tool cover -html=$@.out -o $@.html
	rm $@.out

# Include all .mk files so you can have your own local configurations
include $(wildcard *.mk)
