.PHONY: build test vet lint install acctest

build:
	go build -o terraform-provider-zimaos.exe ./zimaos

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

# Acceptance tests require a running dockur/zima container and TF_ACC=1
acctest:
	TF_ACC=1 go test ./... -v

install: build
	mkdir -p ~/.terraform.d/plugins/example.com/example/zimaos/0.1.0/windows_amd64
	cp terraform-provider-zimaos.exe ~/.terraform.d/plugins/example.com/example/zimaos/0.1.0/windows_amd64/
