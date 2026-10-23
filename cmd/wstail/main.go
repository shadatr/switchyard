// Command wstail prints live-tail batches from /admin/live (dev helper).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
)

func main() {
	url := flag.String("url", "ws://127.0.0.1:8787/admin/live", "live tail URL")
	token := flag.String("token", os.Getenv("SWITCHYARD_ADMIN_TOKEN"), "admin token")
	dur := flag.Duration("for", 3*time.Second, "how long to listen")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), *dur)
	defer cancel()
	c, _, err := websocket.Dial(ctx, *url+"?token="+*token, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20) // batches of up to 200 events exceed the 32 KiB default
	batches, events := 0, 0
	for {
		_, msg, err := c.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, "read:", err)
			}
			break
		}
		var b struct {
			Events  []map[string]any `json:"events"`
			Skipped int64            `json:"skipped"`
		}
		_ = json.Unmarshal(msg, &b)
		batches++
		events += len(b.Events)
		if batches == 1 && len(b.Events) > 0 {
			e, _ := json.Marshal(b.Events[0])
			fmt.Println("first event:", string(e))
		}
	}
	fmt.Printf("received %d batches, %d events\n", batches, events)
}
