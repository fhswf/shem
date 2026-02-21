// shem_testmodule - Reference implementation and test for a SHEM module

package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fhswf/shem/shemmsg" // using this library is optional, see below
)

const (
	// logging levels (see sd-daemon(3))
	LogDebug   = "<7>"
	LogInfo    = "<6>"
	LogWarning = "<4>"
	LogErr     = "<3>"
)

// log writes a message to stderr for systemd logging
func log(priority, message string) {
	fmt.Fprintf(os.Stderr, "%s%s\n", priority, message)
}

var writer = shemmsg.NewWriter(os.Stdout)

// sendPointValue sends a properly formatted pointvalue message to stdout
func sendPointValue(name string, value float64) error {
	// you can manually construct the message:
	/*	message := fmt.Sprintf("\n\npointvalue %s\n%.1f\n\n", name, value)
		_, err := fmt.Print(message)
		return err */

	// or you can use the shemmsg library:
	v, err := shemmsg.Number(value)
	if err != nil {
		return err
	}
	return writer.Write(shemmsg.Message{
		Name:    name,
		Payload: shemmsg.PointValue{Value: v},
	})
}

// monitorStdin watches for stdin closure (EOF) which signals shutdown
func monitorStdin(shutdownChan chan<- struct{}) {
	// if no incoming messages are expected, just wait for stdin to close:
	/*	scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			log(LogDebug, fmt.Sprintf("Received input: %q", line))
		}
	*/

	// otherwise, you can use the shemmsg library to parse messages:
	reader := shemmsg.NewReader(os.Stdin)
	for {
		msg, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log(LogWarning, fmt.Sprintf("Error reading message: %v", err))
			continue
		}
		log(LogDebug, fmt.Sprintf("Received message: %s %s", msg.Type(), msg.Name))
	}

	log(LogInfo, "Stdin closed, initiating shutdown")
	close(shutdownChan)
}

// sendPeriodicValues sends test_power values every 10 seconds
func sendPeriodicValues(shutdownChan <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// function for sending the value
	sendValue := func() {
		currentTime := time.Now().UTC()
		seconds := float64(currentTime.Second())

		if err := sendPointValue("test_power", seconds); err != nil {
			log(LogErr, fmt.Sprintf("Failed to send pointvalue: %v", err))
		}
	}

	// send initial value immediately
	sendValue()

	for {
		select {
		case <-ticker.C:
			sendValue()

		case <-shutdownChan:
			return
		}
	}
}

func main() {
	log(LogInfo, "Test module starting")

	// channel for shutdown signal
	shutdownChan := make(chan struct{})

	// system signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// start go routines
	go monitorStdin(shutdownChan)
	go sendPeriodicValues(shutdownChan)

	// wait for shutdown signal
	select {
	case <-shutdownChan:
		log(LogInfo, "Shutting down")

	case sig := <-sigChan:
		log(LogWarning, fmt.Sprintf("Received signal %v, shutting down", sig))
	}

	log(LogInfo, "Test module stopped.")
}
