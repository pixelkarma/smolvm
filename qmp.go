package main

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

type qmpCommand struct {
	Execute string `json:"execute"`
}

type qmpResponse struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
}

func qmpSystemPowerdown(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	// Read QMP greeting.
	var greeting map[string]json.RawMessage
	if err := dec.Decode(&greeting); err != nil {
		return fmt.Errorf("read qmp greeting: %w", err)
	}

	if err := qmpExecute(dec, enc, "qmp_capabilities"); err != nil {
		return err
	}
	if err := qmpExecute(dec, enc, "system_powerdown"); err != nil {
		return err
	}
	return nil
}

func qmpExecute(dec *json.Decoder, enc *json.Encoder, command string) error {
	if err := enc.Encode(qmpCommand{Execute: command}); err != nil {
		return fmt.Errorf("send %s: %w", command, err)
	}
	var resp qmpResponse
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("read %s response: %w", command, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("qmp %s failed: %s", command, resp.Error.Desc)
	}
	return nil
}
