.PHONY: build clean deploy gomodgen

build: gomodgen
	export GO111MODULE=on
	env GOARCH=amd64 GOOS=linux go build -ldflags="-s -w" -o bin/eventconsumer eventConsumer/main.go
	env GOARCH=amd64 GOOS=linux go build -ldflags="-s -w" -o bin/checkrule checkRule/main.go

clean:
	rm -rf ./bin ./vendor go.sum

deploy: clean build
	sls deploy --verbose

gomodgen:
	chmod u+x gomod.sh
	./gomod.sh
	go mod tidy

event:
	@while [ true ]; do \
		curl -i test-waf-863570124.ap-northeast-1.elb.amazonaws.com; \
		sleep 3; \
	done; \
	true