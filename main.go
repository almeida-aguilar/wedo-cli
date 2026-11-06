// wedo-cli: interactive explorer for the LEGO WeDo 2.0 Smart Hub over BLE.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	"wedo-cli/internal/ble"
	"wedo-cli/internal/wedo"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	switch args[0] {
	case "scan":
		return cmdScan(args[1:])
	case "help", "-h", "--help":
		usage()
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
	usage()
	return 2
}

func usage() {
	fmt.Fprint(os.Stderr, `wedo-cli: LEGO WeDo 2.0 BLE explorer

Usage:
  wedo-cli scan [-t 30s] [-id ID] [-v]   scan, connect to first hub, and open shell
  wedo-cli scan -l [-all] [-t 8s]        only list hubs (or all devices with -all)

Inside the shell, type "help" to see available commands.
`)
}

func isWeDo(a ble.Advert) bool {
	for _, s := range a.Services {
		if s == ble.WeDoServiceUUID {
			return true
		}
	}
	return strings.HasPrefix(a.Name, "LPF2 Smart Hub")
}

func cmdScan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Duration("t", 0, "maximum scan duration (30s to connect, 8s to list)")
	list := fs.Bool("l", false, "only list found hubs")
	all := fs.Bool("all", false, "with -l: list all BLE devices")
	id := fs.String("id", "", "connect to a specific hub (ID/MAC from scan)")
	verbose := fs.Bool("v", false, "debug logging (writes and notifications)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	be, err := ble.NewPlatformBackend(log)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *list {
		d := *timeout
		if d == 0 {
			d = 8 * time.Second
		}
		if err := listHubs(ctx, be, *all, d); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
		return 0
	}

	d := *timeout
	if d == 0 {
		d = 30 * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()

	fmt.Print("Searching for hub... ")
	adv, err := findHub(sctx, be, *id)
	if err != nil {
		fmt.Println("not found.")
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Printf("found (%s, %s, %d dBm). ", displayName(adv), adv.ID, adv.RSSI)

	hub, err := wedo.Open(be, adv.ID, log)
	if err != nil {
		fmt.Println()
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Println("Connected.")
	fmt.Println()
	stop() // the shell installs its own Ctrl+C handling

	newShell(hub, os.Stdout).Run()
	return 0
}

func displayName(a ble.Advert) string {
	if a.Name == "" {
		return "unnamed"
	}
	return a.Name
}

// findHub scans until the first matching advert (any WeDo hub, or the one
// with the given ID) and returns once the scan has fully stopped.
func findHub(ctx context.Context, be ble.Backend, id string) (ble.Advert, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	found := make(chan ble.Advert, 1)
	done := make(chan error, 1)
	go func() {
		done <- be.Scan(sctx, func(a ble.Advert) {
			if id != "" {
				if !strings.EqualFold(a.ID, id) {
					return
				}
			} else if !isWeDo(a) {
				return
			}
			select {
			case found <- a:
				cancel()
			default:
			}
		})
	}()
	err := <-done
	select {
	case a := <-found:
		return a, nil
	default:
	}
	if err != nil {
		return ble.Advert{}, err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ble.Advert{}, errors.New("timed out: no hub found (is it powered on and not connected elsewhere?)")
	}
	return ble.Advert{}, errors.New("search canceled")
}

func listHubs(ctx context.Context, be ble.Backend, all bool, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	var mu sync.Mutex
	seen := map[string]ble.Advert{}
	err := be.Scan(ctx, func(a ble.Advert) {
		if !all && !isWeDo(a) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if old, ok := seen[a.ID]; ok {
			if a.Name == "" {
				a.Name = old.Name
			}
			if len(a.Services) == 0 {
				a.Services = old.Services
			}
		}
		seen[a.ID] = a
	})
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		fmt.Println("No devices found.")
		return nil
	}
	adverts := make([]ble.Advert, 0, len(seen))
	for _, a := range seen {
		adverts = append(adverts, a)
	}
	sort.Slice(adverts, func(i, j int) bool { return adverts[i].RSSI > adverts[j].RSSI })
	for _, a := range adverts {
		kind := ""
		if isWeDo(a) {
			kind = "WeDo 2.0"
		}
		fmt.Printf("%-18s %-24s %4d dBm  %s\n", a.ID, displayName(a), a.RSSI, kind)
	}
	return nil
}
