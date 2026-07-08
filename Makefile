.PHONY: build test fmt vet install clean

build:
	go build -o bin/taskman .

test:
	go test ./...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

install:
	go install .

clean:
	rm -rf bin
