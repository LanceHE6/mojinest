BIN := mojinest
DESKTOP := mojinest-desktop
BIN_PATH := /usr/local/bin/mojinest
DIST := dist

.PHONY: build test vet fmt run clean docker install sync-bin desktop desktop-all clean-dist release

build:
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/mojinest

# 桌面客户端（NOCGO：单文件、无运行时依赖，可直接拷到别的机器）
desktop:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(DESKTOP) ./cmd/mojinest-desktop

# 三平台产物，放进 dist/
release: clean-dist
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(DESKTOP)-linux-amd64     ./cmd/mojinest-desktop
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(DESKTOP)-windows-amd64.exe ./cmd/mojinest-desktop
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o $(DIST)/$(DESKTOP)-darwin-arm64    ./cmd/mojinest-desktop
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
