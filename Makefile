.PHONY: test integration-test lint build clean completions man

test:
	go test ./...

integration-test:
	go test -v -count=1 -tags=integration ./integration

lint:
	golangci-lint run
	hk check --all --check

build:
	go build -o ./dist/dots .

completions: build
	mkdir -p ./dist/completions
	./dist/dots completion bash > ./dist/completions/dots.bash
	./dist/dots completion zsh > ./dist/completions/_dots
	./dist/dots completion fish > ./dist/completions/dots.fish

man: build
	mkdir -p ./dist/man
	./dist/dots man ./dist/man

clean:
	rm -rf ./dist
