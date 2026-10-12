BIN := mojinest
BIN_PATH := /usr/local/bin/mojinest
DIST := dist

.PHONY: build test vet fmt run clean docker install release clean-dist

build:
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/mojinest

# 跨平台发布包（服务端）
release: clean-dist
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/mojinest-linux-amd64        ./cmd/mojinest
	ls -la $(DIST)

clean-dist:
	rm -rf $(DIST)

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
	rm -f $(BIN) $(DESKTOP)

# 把本地编译产物推到服务器（开发期热更新）
install: build
	scp $(BIN) $(HOST):$(BIN_PATH).new && ssh $(HOST) \
	  'sudo mv $(BIN_PATH).new $(BIN_PATH) && sudo systemctl restart mojinest'
