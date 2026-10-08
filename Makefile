BIN := mojinest
BIN_PATH := /usr/local/bin/mojinest

.PHONY: build test vet fmt run clean docker install sync-bin

build:
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/mojinest

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

run: build
	./$(BIN) -config $${MOJINEST_CONFIG:-$$HOME/.config/mojinest/config.json}

docker:
	docker compose up -d --build

clean:
	rm -f $(BIN)

# 把本地编译产物推到服务器（开发期热更新）
install: build
	scp $(BIN) $(HOST):$(BIN_PATH).new && ssh $(HOST) \
	  'sudo mv $(BIN_PATH).new $(BIN_PATH) && sudo systemctl restart mojinest'
