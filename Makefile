APP = wpback
DIST = dist
GO = go

.PHONY: all build build-all clean

# build همه معماری‌ها
build:
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o $(DIST)/$(APP)_linux_amd64 main.go
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o $(DIST)/$(APP)_linux_arm64 main.go
	GOOS=linux GOARCH=arm CGO_ENABLED=0 $(GO) build -o $(DIST)/$(APP)_linux_arm main.go

clean:
	rm -rf $(DIST)
