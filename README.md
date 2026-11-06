# wedo-cli

Interactive BLE explorer for the LEGO WeDo 2.0 Smart Hub (Go, BlueZ via TinyGo bluetooth).

    go mod tidy
    go test ./internal/wedo
    go build -o wedo-cli .
    ./wedo-cli scan            # connect to first hub and open shell
    ./wedo-cli scan -l         # list hubs
    ./wedo-cli scan -v         # debug log of each write/notification

Linux only (BlueZ). `help` inside any prompt shows available commands.
Extras on top of spec: `current`, `info`, `name`, `vcc`, `listen all`,
`motor A brake`, `led list`, `distance|tilt [A|B]`, and the `raw` menu
(`gatt`, `read`, `write`, `notify`, `input`, `output`).

Unverified on hardware: tilt direction names, count/bump mode value format,
counter `reset`, and voltage/current units.
Use `listen` (displays bytes in hex) and `raw` to verify them.
