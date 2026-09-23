# logrusx

Asynchronous JSON logger for services, built on top of [logrus](https://github.com/sirupsen/logrus).

## Usage

```go
package main

import (
	"github.com/Uspacy/logrusx"
	"github.com/sirupsen/logrus"
)

func main() {
	log, err := logrusx.New("my-service", logrusx.WithLevel(logrus.DebugLevel))
	if err != nil {
		panic(err)
	}
	// Flushes queued messages before exit
	defer log.Close()

	log.Info("service started", logrusx.LogField{Key: "port", Value: 8080})
	log.Warning("cache is disabled")
}
```

Messages are written by a background goroutine. Call `Close` before the program
exits, otherwise queued messages may be lost. `defer` does not run on `os.Exit`,
so close the logger explicitly before calling it.

`Fatal` waits until all previous messages and the fatal message are written, then
exits the process with code 1.

### Options

- `WithLevel(level)` — minimum level to write (default: `logrus.InfoLevel`).
- `WithOutput(w)` — destination writer (default: `os.Stdout`).
