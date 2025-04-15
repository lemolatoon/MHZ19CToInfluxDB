package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"go.bug.st/serial"
)

func initConn() (io.ReadWriteCloser, error) {
	// device name from env variable, if not set uartreg.Open will open the first available device
	uartport := os.Getenv("UART_DEV")
	if uartport == "" {
		ports, err := serial.GetPortsList()
		if err != nil {
			return nil, fmt.Errorf("failed to get serial ports: %v", err)
		}
		if len(ports) == 0 {
			return nil, fmt.Errorf("no serial ports found")
		}
		uartport = ports[0]
	}
	log.Printf("UART device: %s", uartport)
	mode := &serial.Mode{
		BaudRate: 9600,
		Parity:   serial.NoParity,
		DataBits: 8,
		StopBits: serial.OneStopBit,
	}
	p, err := serial.Open(uartport, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to open UART port %s: %v", uartport, err)
	}

	return p, nil
}

type Result struct {
	Co2Concentration float32
}

const cmdSize = 9

// Byte0: 0xFF, Byte1: 0x01, Byte2: 0x86, Byte3～7: 0x00, Byte8: Checksum
func buildCommand() []byte {
	cmd := make([]byte, cmdSize)
	cmd[0] = 0xFF
	cmd[1] = 0x01
	cmd[2] = 0x86
	for i := 3; i < 8; i++ {
		cmd[i] = 0x00
	}
	sum := 0
	for i := 1; i < 8; i++ {
		sum += int(cmd[i])
	}
	cmd[8] = byte(0xFF - sum + 1)
	return cmd
}

func checksum(response []byte) byte {
	sum := 0
	for i := 1; i < 8; i++ {
		sum += int(response[i])
	}
	return byte(0xFF - sum + 1)
}

func read(dev io.ReadWriter, cmd []byte) (Result, error) {
	response := make([]byte, cmdSize)

	n, err := dev.Write(cmd)
	if err != nil {
		return Result{}, fmt.Errorf("failed to send command: %v", err)
	}
	if n != len(cmd) {
		return Result{}, fmt.Errorf("failed to send command: %d bytes sent, expected %d", n, len(cmd))
	}

	time.Sleep(150 * time.Millisecond)

	_, err = dev.Read(response)
	if err != nil {
		return Result{}, fmt.Errorf("failed to read response: %v", err)
	}

	if len(response) < cmdSize {
		return Result{}, fmt.Errorf("response too short: %d bytes, expected %d", len(response), cmdSize)
	}
	if response[0] != 0xFF || response[1] != 0x86 {
		return Result{}, fmt.Errorf("invalid response header: %02X %02X", response[0], response[1])
	}

	if response[8] != checksum(response) {
		return Result{}, fmt.Errorf("invalid checksum: %02X", response[8])
	}

	high := int(response[2])
	low := int(response[3])
	concentration := float32(high*256 + low)
	return Result{Co2Concentration: concentration}, nil
}

func main() {
	c, err := initConn()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	cmd := buildCommand()
	for {
		res, err := read(c, cmd)
		if err != nil {
			log.Printf("Error: %v", err)
			continue
		}
		log.Printf("CO2 Concentration: %.2f ppm", res.Co2Concentration)
		time.Sleep(1 * time.Second)
	}
}
