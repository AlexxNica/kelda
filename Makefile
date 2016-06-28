export GO15VENDOREXPERIMENT=1
PACKAGES=$(shell GO15VENDOREXPERIMENT=1 go list ./... | grep -v vendor)
NOVENDOR=$(shell find . -path ./vendor -prune -o -name '*.go' -print)
LINE_LENGTH_EXCLUDE=./cluster/provider/constraintConstants.go ./cluster/provider/gceConstants.go ./minion/pb/pb.pb.go ./cluster/provider/cloud_config.go ./minion/network/link_test.go

REPO = quilt
DOCKER = docker
SHELL := /bin/bash

all: inspect
	cd -P . && \
	go build . && \
	go build -o ./minion/minion ./minion

install:
	cd -P . && go install .

generate:
	go generate $(PACKAGES)

providers:
	python3 scripts/gce-descriptions > provider/gceConstants.go

format:
	gofmt -w -s $(NOVENDOR)
	python scripts/format-check.py $(filter-out $(LINE_LENGTH_EXCLUDE),$(NOVENDOR))

format-check:
	RESULT=`gofmt -s -l $(NOVENDOR)` && \
	if [[ -n "$$RESULT"  ]] ; then \
	    echo $$RESULT && \
	    exit 1 ; \
	fi

check:
	go test $(PACKAGES)

lint: format
	cd -P . && go vet $(PACKAGES)
	for package in $(PACKAGES) ; do \
		if [[ $$package != *minion/pb* ]] ; then \
			golint -min_confidence .25 $$package ; \
		fi \
	done

inspect:
	cd -P . && \
	go build -o ./inspect/inspect ./inspect

.PHONY: ./inspect

COV_SKIP= /minion/pb /minion/pprofile

COV_PKG = $(subst github.com/NetSys/quilt,,$(PACKAGES))
coverage: $(addsuffix .cov, $(filter-out $(COV_SKIP), $(COV_PKG)))
	gover

%.cov:
	go test -coverprofile=.$@.coverprofile .$*
	go tool cover -html=.$@.coverprofile -o .$@.html

# BUILD
docker-build-all: docker-build-tester docker-build-minion docker-build-ovs

docker-build-tester:
	cd -P quilt-tester && ${DOCKER} build -t ${REPO}/tester .

docker-build-minion:
	cd -P minion && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build . \
	 && ${DOCKER} build -t ${REPO}/minion .

docker-build-ovs:
	cd -P ovs && docker build -t ${REPO}/ovs .

# PUSH
#
docker-push-all: docker-push-tester docker-push-minion
	# We do not push the OVS container as it's built by the automated
	# docker hub system.

docker-push-tester:
	${DOCKER} push ${REPO}/tester

docker-push-minion:
	${DOCKER} push ${REPO}/minion

docker-push-ovs:
	${DOCKER} push ${REPO}/ovs

# Include all .mk files so you can have your own local configurations
include $(wildcard *.mk)
