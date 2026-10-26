package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"clashgo/internal/core"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.SetName("Clash Go")
	mygo.App.SetVersion("0.1.0")
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	manager, err := core.NewManager(filepath.Join(dir, "clash-go"))
	if err != nil {
		log.Fatal(err)
	}
	settings := manager.Settings()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer manager.Stop()
	s := &desktop{manager: manager, ctx: ctx, port: strconv.Itoa(settings.Port), page: "overview", state: manager.Snapshot()}
	mygo.App.WhenReady(func() {
		s.window = mygo.NewWindow(mygo.WindowOptions{
			Title: "Clash Go", Width: 1080, Height: 760, MinWidth: 900, MinHeight: 650,
			Content: ui.View(s.view),
		})
		s.run(func() error { return manager.Initialize(ctx, s.reportKernel) }, "内核已就绪；已有配置自动启动，否则请先导入配置")
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.window.Update(func() {
						if !s.busy && !s.closing {
							s.run(func() error { return nil }, "")
						}
					})
				}
			}
		}()
	})
	mygo.App.OnSecondInstance(func([]string, string) {
		if s.window != nil {
			s.window.Restore()
			s.window.Focus()
		}
	})
	readyToQuit := false
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if readyToQuit {
			return
		}
		e.PreventDefault()
		if s.closing {
			return
		}
		s.closing = true
		cancel()
		go func() {
			if err := manager.Stop(); err != nil {
				log.Printf("停止内核失败: %v", err)
			}
			mygo.RunOnMain(func() { readyToQuit = true; mygo.App.Quit() })
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Print(err)
	}
}
