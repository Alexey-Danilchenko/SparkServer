// Package monitorcli implements the sparkctl command-line operator interface.
package monitorcli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

// Options carries CLI defaults and injectable IO/HTTP dependencies for tests.
type Options struct {
	BaseURL    string
	Token      string
	Username   string
	Password   string
	JSONOutput bool
	Out        io.Writer
	Err        io.Writer
	HTTPClient *http.Client
}

// Run parses sparkctl arguments and dispatches monitoring or live-device commands.
func Run(ctx context.Context, args []string, options Options) error {
	if options.Out == nil {
		options.Out = os.Stdout
	}
	if options.Err == nil {
		options.Err = os.Stderr
	}
	options = options.withEnvDefaults()

	flags := flag.NewFlagSet("sparkctl", flag.ContinueOnError)
	flags.SetOutput(options.Err)
	flags.StringVar(&options.BaseURL, "base", options.BaseURL, "Spark server base URL")
	flags.StringVar(&options.Token, "token", options.Token, "Bearer token")
	flags.StringVar(&options.Username, "username", options.Username, "Username for automatic login")
	flags.StringVar(&options.Password, "password", options.Password, "Password for automatic login")
	flags.BoolVar(&options.JSONOutput, "json", options.JSONOutput, "Print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	remaining := flags.Args()
	if len(remaining) == 0 {
		printUsage(options.Err)
		return flag.ErrHelp
	}
	if remaining[0] == "help" || remaining[0] == "-help" || remaining[0] == "--help" {
		printUsage(options.Out)
		return nil
	}

	client, err := NewClient(options.BaseURL, options.Token, options.Username, options.Password, options.HTTPClient)
	if err != nil {
		return err
	}

	// Commands intentionally mirror the small set needed for local hardware smoke tests.
	switch remaining[0] {
	case "login":
		token, err := client.Login(ctx)
		if err != nil {
			return err
		}
		return printValue(options.Out, options.JSONOutput, map[string]string{"access_token": token}, token)
	case "devices":
		devices, err := client.ListDevices(ctx)
		if err != nil {
			return err
		}
		return printDevices(options.Out, options.JSONOutput, devices)
	case "device":
		if len(remaining) != 2 {
			return fmt.Errorf("usage: sparkctl device <device-id-or-name>")
		}
		device, err := client.GetDevice(ctx, remaining[1])
		if err != nil {
			return err
		}
		return printDevice(options.Out, options.JSONOutput, device)
	case "ping":
		if len(remaining) != 2 {
			return fmt.Errorf("usage: sparkctl ping <device-id-or-name>")
		}
		result, err := client.Ping(ctx, remaining[1])
		if err != nil {
			return err
		}
		return printValue(options.Out, options.JSONOutput, result, fmt.Sprintf("%s online=%t", result.ID, result.Online))
	case "variable", "var":
		if len(remaining) != 3 {
			return fmt.Errorf("usage: sparkctl variable <device-id-or-name> <variable>")
		}
		result, err := client.GetVariable(ctx, remaining[1], remaining[2])
		if err != nil {
			return err
		}
		return printValue(options.Out, options.JSONOutput, result, fmt.Sprintf("%s=%s", result.Name, result.Result))
	case "function", "call":
		if len(remaining) < 3 || len(remaining) > 4 {
			return fmt.Errorf("usage: sparkctl function <device-id-or-name> <function> [argument]")
		}
		argument := ""
		if len(remaining) == 4 {
			argument = remaining[3]
		}
		result, err := client.CallFunction(ctx, remaining[1], remaining[2], argument)
		if err != nil {
			return err
		}
		return printValue(options.Out, options.JSONOutput, result, fmt.Sprintf("%s returned %d", result.Name, result.ReturnValue))
	case "events":
		return runEvents(ctx, options, client, remaining[1:])
	default:
		return fmt.Errorf("unknown command %q", remaining[0])
	}
}

func runEvents(ctx context.Context, options Options, client *Client, args []string) error {
	flags := flag.NewFlagSet("sparkctl events", flag.ContinueOnError)
	flags.SetOutput(options.Err)
	deviceID := flags.String("device", "", "Only stream events for one device")
	prefix := flags.String("prefix", "", "Only stream events with this prefix")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("usage: sparkctl events [-device <device-id-or-name>] [-prefix <prefix>]")
	}
	return client.StreamEvents(ctx, *deviceID, *prefix, func(event Event) error {
		if options.JSONOutput {
			return printJSON(options.Out, event)
		}
		_, err := fmt.Fprintf(options.Out, "%s %s %s\n", event.PublishedAt, event.CoreID, event.Name)
		if err != nil {
			return err
		}
		if event.Data != "" {
			_, err = fmt.Fprintf(options.Out, "  %s\n", event.Data)
		}
		return err
	})
}

func (options Options) withEnvDefaults() Options {
	if options.BaseURL == "" {
		options.BaseURL = os.Getenv("SPARK_SERVER_URL")
	}
	if options.Token == "" {
		options.Token = os.Getenv("SPARK_TOKEN")
	}
	if options.Username == "" {
		options.Username = os.Getenv("SPARK_USERNAME")
	}
	if options.Password == "" {
		options.Password = os.Getenv("SPARK_PASSWORD")
	}
	return options
}

func printDevices(writer io.Writer, jsonOutput bool, devices []Device) error {
	if jsonOutput {
		return printJSON(writer, devices)
	}
	if len(devices) == 0 {
		_, err := fmt.Fprintln(writer, "No devices claimed.")
		return err
	}
	_, err := fmt.Fprintln(writer, "ID\tNAME\tONLINE\tPRODUCT\tVARIABLES\tFUNCTIONS")
	if err != nil {
		return err
	}
	for _, device := range devices {
		_, err := fmt.Fprintf(writer, "%s\t%s\t%t\t%s\t%s\t%s\n", device.ID, device.Name, device.Online || device.Connected, device.ProductID, strings.Join(variableNames(device.Variables), ","), strings.Join(device.Functions, ","))
		if err != nil {
			return err
		}
	}
	return nil
}

func printDevice(writer io.Writer, jsonOutput bool, device *Device) error {
	if jsonOutput {
		return printJSON(writer, device)
	}
	_, err := fmt.Fprintf(writer, "ID: %s\nName: %s\nOnline: %t\nProduct: %s\nVariables: %s\nFunctions: %s\n", device.ID, device.Name, device.Online || device.Connected, device.ProductID, strings.Join(variableNames(device.Variables), ","), strings.Join(device.Functions, ","))
	return err
}

func printValue(writer io.Writer, jsonOutput bool, value any, text string) error {
	if jsonOutput {
		return printJSON(writer, value)
	}
	_, err := fmt.Fprintln(writer, text)
	return err
}

func printJSON(writer io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(writer, string(encoded))
	return err
}

func variableNames(variables map[string]string) []string {
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: sparkctl [flags] <command> [args]

Flags:
  -base URL       Spark server base URL (default http://localhost:8080 or SPARK_SERVER_URL)
  -token TOKEN    Bearer token (or SPARK_TOKEN)
  -username USER  Username for automatic login (or SPARK_USERNAME)
  -password PASS  Password for automatic login (or SPARK_PASSWORD)
  -json           Print JSON output

Commands:
  login                                 Print an access token
  devices                               List claimed devices
  device <device>                       Show device variables/functions/status
  ping <device>                         Ping a connected device
  variable <device> <name>              Read a device variable
  function <device> <name> [argument]   Call a device function
  events [-device <device>] [-prefix p] Stream server-sent events`)
}
