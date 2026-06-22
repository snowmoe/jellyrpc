.PHONY: build install uninstall clean

BINARY_NAME=jellyrpc
BUILD_DIR=$(HOME)/.local/bin
SYSTEMD_DIR=$(HOME)/.config/systemd/user

build:
	@echo "compiling binary"
	go build -ldflags="-s -w" -o $(BINARY_NAME) .

install: build
	@echo "creating deployment dirs"
	mkdir -p $(BUILD_DIR)
	mkdir -p $(SYSTEMD_DIR)

	@echo "installing binary to $(BUILD_DIR)"
	cp $(BINARY_NAME) $(BUILD_DIR)/$(BINARY_NAME)

	@echo "installing systemd user service"
	cp $(BINARY_NAME).service $(SYSTEMD_DIR)/$(BINARY_NAME).service

	@echo "setting up config"
	mkdir -p $(HOME)/.config/jellyrpc
	cp --update=none config.example $(HOME)/.config/jellyrpc/config

	@echo "reloading systemd user daemon"
	systemctl --user daemon-reload

	@echo "setup complete, run 'systemctl --user enable --now $(BINARY_NAME)' to start."

uninstall:
	@echo "stopping and disabling services"
	-systemctl --user disable --now $(BINARY_NAME)

	@echo "removing binary and service files"
	rm -f $(BUILD_DIR)/$(BINARY_NAME)
	rm -f $(SYSTEMD_DIR)/$(BINARY_NAME).service

	@echo "reloading systemd user daemon"
	systemctl --user daemon-reload
	@echo "uninstalled cleanly"

clean:
	@echo "cleaning build artifacts"
	rm -f $(BINARY_NAME)