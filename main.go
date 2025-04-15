package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api/write"

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

func initInfo() (InfluxDBInfo, error) {
	org, found := os.LookupEnv("INFLUXDB_ORG")
	if !found {
		org = "lemolatoon"
	}
	bucket, found := os.LookupEnv("INFLUXDB_BUCKET")
	if !found {
		bucket = "sensor-home"
	}
	return InfluxDBInfo{Org: org, Bucket: bucket}, nil
}

type InfluxDBInfo struct {
	Org    string
	Bucket string
}

func initLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		log.Printf("Failed to laod Asia/Tokyo timezone: %v", err)
		loc = time.FixedZone("Asia/Tokyo", 9*60*60) // fallback
	}
	return loc
}

func send(client influxdb2.Client, info InfluxDBInfo, loc *time.Location, result *Result) {
	writeAPI := client.WriteAPIBlocking(info.Org, info.Bucket)
	tags := map[string]string{
		"sensor": "MH-Z19C",
	}
	fields := map[string]interface{}{
		"co2_concentration": result.Co2Concentration,
	}
	point := write.NewPoint("sensor_data", tags, fields, time.Now().In(loc))

	if err := writeAPI.WritePoint(context.Background(), point); err != nil {
		log.Printf("Error writing point: %v", err)
	}
}

func initSleepDuration() time.Duration {
	durationStr, found := os.LookupEnv("SLEEP_DURATION_SECONDS")
	if !found {
		durationStr = "60"
	}
	duration, err := strconv.Atoi(durationStr)
	if err != nil {
		log.Printf("Invalid SLEEP_DURATION_SECONDS value: %v, defaulting to 60 seconds", err)
		duration = 60
	}
	if duration <= 0 {
		log.Printf("SLEEP_DURATION_SECONDS must be positive, defaulting to 60 seconds")
		duration = 60
	}
	return time.Duration(duration) * time.Second
}

func initClient() (influxdb2.Client, error) {
	token, found := os.LookupEnv("INFLUXDB_TOKEN")
	if !found {
		return nil, fmt.Errorf("INFLUXDB_TOKEN not set")
	}
	url, found := os.LookupEnv("INFLUXDB_URL")
	if !found {
		url = "http://localhost:8086"
	}
	return influxdb2.NewClient(url, token), nil
}

func doIt(c io.ReadWriter, cmd []byte, client influxdb2.Client, info InfluxDBInfo, loc *time.Location) {
	result, err := read(c, cmd)
	if err != nil {
		log.Printf("Error reading data: %v", err)
		return
	}
	log.Printf("CO2 Concentration: %.2f ppm", result.Co2Concentration)
	send(client, info, loc, &result)
}

func main() {
	c, err := initConn()
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	loc := initLocation()
	influxInfo, err := initInfo()
	if err != nil {
		log.Fatal(err)
	}
	client, err := initClient()
	if err != nil {
		log.Fatal(err)
	}
	sleepDuration := initSleepDuration()

	cmd := buildCommand()
	for {
		go doIt(c, cmd, client, influxInfo, loc)
		time.Sleep(sleepDuration)
	}
}
