package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/codegangsta/cli"

	"github.com/jvns/multi-gotty/app"
)

func main() {
	cmd := cli.NewApp()
	cmd.Version = app.Version
	cmd.Name = "multi-gotty"
	cmd.Usage = "Share many terminals as a web application"
	cmd.HideHelp = true

	cmd.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "address",
			Value: "127.0.0.1",
			Usage: "ip address to listen on",
		},
		cli.StringFlag{
			Name:  "port",
			Value: "8080",
			Usage: "port to listen on",
		},
		cli.StringFlag{
			Name:  "index-dir",
			Value: "",
			Usage: "directory to serve statics from",
		},
		cli.StringFlag{
			Name:  "ws-origin",
			Value: "",
			Usage: "directory to serve statics from",
		},
	}

	cmd.Action = func(c *cli.Context) {
		options := app.DefaultOptions
		options.Address = c.String("address")
		options.Port = c.String("port")
		options.IndexFile = c.String("index-dir")
		options.WSOrigin = c.String("ws-origin")
        options.PermitWrite = true
		if len(c.Args()) != 1 {
			fmt.Println("Error: No command given.\n")
			cli.ShowAppHelp(c)
			exit(nil, 1)
		}
		commandServer := c.Args().Get(0)

		app, err := app.New(commandServer, &options)
		if err != nil {
			exit(err, 3)
		}

		registerSignals(app)

		err = app.Run()
		if err != nil {
			exit(err, 4)
		}
	}

	cli.AppHelpTemplate = helpTemplate

	cmd.Run(os.Args)
}

func exit(err error, code int) {
	if err != nil {
		fmt.Println(err)
	}
	os.Exit(code)
}

func registerSignals(app *app.App) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(
		sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	go func() {
		for {
			s := <-sigChan
			switch s {
			case syscall.SIGINT, syscall.SIGTERM:
				if app.Exit() {
					fmt.Println("Send ^C to force exit.")
				} else {
					os.Exit(5)
				}
			}
		}
	}()
}
