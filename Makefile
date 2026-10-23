BINARY := bin/ci-server
ifeq ($(OS),Windows_NT)
	BINARY := bin/ci-server.exe
endif

.PHONY: all build run vet tidy test release clean clean-web web-install web-build web-dev

all: web-build build

## 编译后端（前端未内嵌时，需先执行 web-build）
build:
	go build -o $(BINARY) ./cmd/server

## 启动后端（默认 :8080，数据目录 ./data）
run:
	go run ./cmd/server

## 静态检查
vet:
	go vet ./...

## 单元测试
test:
	go test ./...

tidy:
	go mod tidy

## 前端依赖
web-install:
	cd web && npm install

## 构建前端到 web/dist（会被 go:embed 内嵌进二进制）
web-build:
	cd web && npm run build

## 前端开发服务器（/api 与 /ws 代理到 :8080）
web-dev:
	cd web && npm run dev

## 一键发布：前端构建 + 交叉编译 linux/windows + UPX 压缩
## 可通过 ARGS 透传参数，例如：make release ARGS="--no-upx"
release:
	bash ./build.sh $(ARGS)

## 清理构建产物（保留 web/dist，避免破坏 go:embed 前提）
clean:
	rm -rf bin data dist

## 彻底清理前端产物（清理后须先执行 web-build 才能编译后端）
clean-web:
	rm -rf web/dist web/node_modules

dist: web-build build
