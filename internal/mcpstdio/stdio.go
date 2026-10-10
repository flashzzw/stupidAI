package mcpstdio

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Streams belong to this helper invocation. Run closes In when the process
// ends; it must not be a reader shared with another protocol consumer.
type Streams struct {
	In  io.ReadCloser
	Out io.Writer
	Err io.Writer
}

func Run(ctx context.Context, binding Binding, command string, args []string, streams Streams) error {
	if streams.In == nil || streams.Out == nil || streams.Err == nil {
		return fmt.Errorf("MCP stdio streams are missing")
	}
	defer streams.In.Close()
	if err := binding.verify(command, args, os.LookupEnv); err != nil {
		return err
	}
	childContext, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childContext, command, args...)
	cmd.Dir = binding.Cwd
	cmd.Stderr = streams.Err
	cmd.WaitDelay = time.Second
	configureChild(cmd)
	input, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("cannot open MCP input")
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("cannot open MCP output")
	}
	defer output.Close()
	// Linux parent-death signals refer to the creating thread. Keep it alive
	// until the child has exited, including while another goroutine forwards input.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start the configured MCP process")
	}
	pending := &requests{methods: map[string]string{}}
	finished := make(chan struct{})
	defer close(finished)
	outputDone := make(chan struct{})
	inputDone := make(chan error, 1)
	go func() {
		err := forward(streams.In, input, func(line []byte) []byte {
			pending.remember(line)
			return line
		})
		_ = input.Close()
		if err != nil {
			select {
			case <-outputDone:
				// Closing our input after the child closes stdout is normal
				// transport teardown, not a new caller-side input failure.
				err = nil
			default:
			}
		}
		inputDone <- err
		if err != nil {
			cancel()
			return
		}
		// EOF ends the caller's transport. Give a cooperative server time to
		// finish and exit, then bound an uncooperative process's lifetime.
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-finished:
		case <-timer.C:
			cancel()
		}
	}()
	outErr := forward(output, streams.Out, func(line []byte) []byte { return pending.annotate(line, binding.Stamp) })
	close(outputDone)
	_ = streams.In.Close()
	if outErr != nil {
		cancel()
	}
	timer := time.AfterFunc(time.Second, cancel)
	waitErr := cmd.Wait()
	timer.Stop()
	if ctx.Err() != nil {
		return fmt.Errorf("MCP transport canceled: %w", ctx.Err())
	}
	if outErr != nil {
		return fmt.Errorf("MCP output transport failed")
	}
	if waitErr != nil {
		return fmt.Errorf("configured MCP process exited unsuccessfully")
	}
	select {
	case err := <-inputDone:
		if err != nil {
			return fmt.Errorf("MCP input transport failed")
		}
	default:
	}
	return nil
}

func forward(in io.Reader, out io.Writer, transform func([]byte) []byte) error {
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			frame := transform(line)
			if n, writeErr := out.Write(frame); writeErr != nil {
				return writeErr
			} else if n != len(frame) {
				return io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type requests struct {
	mu      sync.Mutex
	methods map[string]string
}

func requestKey(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return "s:" + name
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil && string(raw) != "null" {
		return "n:" + strconv.FormatInt(number, 10)
	}
	return ""
}

func (r *requests) remember(line []byte) {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if json.Unmarshal(line, &request) != nil || (request.Method != "initialize" && request.Method != "tools/call") {
		return
	}
	key := requestKey(request.ID)
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.methods[key]; exists {
		// A duplicated request ID cannot be attributed to one invocation.
		r.methods[key] = ""
	} else {
		r.methods[key] = request.Method
	}
}

func object(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	value := map[string]json.RawMessage{}
	if len(raw) == 0 || string(raw) == "null" {
		return value, true
	}
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, false
	}
	return value, true
}

func stamped(original json.RawMessage, stamp Stamp) json.RawMessage {
	// The project key is reserved for this transport. Nested transports keep
	// their original value as upstream; all other metadata keys stay intact.
	value := struct {
		Stamp
		Upstream json.RawMessage `json:"upstream,omitempty"`
	}{stamp, original}
	encoded, _ := json.Marshal(value)
	return encoded
}

func (r *requests) annotate(line []byte, stamp Stamp) []byte {
	root, ok := object(line)
	if !ok || len(root) == 0 || len(root["method"]) > 0 {
		return line
	}
	key := requestKey(root["id"])
	if key == "" {
		return line
	}
	r.mu.Lock()
	method := r.methods[key]
	delete(r.methods, key)
	r.mu.Unlock()
	if method == "" || len(root["result"]) == 0 || len(root["error"]) > 0 {
		return line
	}
	result, ok := object(root["result"])
	if !ok || len(result) == 0 {
		return line
	}
	switch method {
	case "initialize":
		capabilities, valid := object(result["capabilities"])
		if !valid || len(result["capabilities"]) == 0 || len(result["protocolVersion"]) == 0 || len(result["serverInfo"]) == 0 {
			return line
		}
		experimental, valid := object(capabilities["experimental"])
		if !valid {
			return line
		}
		experimental[MetadataKey] = stamped(experimental[MetadataKey], stamp)
		capabilities["experimental"], _ = json.Marshal(experimental)
		result["capabilities"], _ = json.Marshal(capabilities)
	case "tools/call":
		metadata, valid := object(result["_meta"])
		if !valid {
			return line
		}
		metadata[MetadataKey] = stamped(metadata[MetadataKey], stamp)
		result["_meta"], _ = json.Marshal(metadata)
	default:
		return line
	}
	root["result"], _ = json.Marshal(result)
	encoded, err := json.Marshal(root)
	if err != nil {
		return line
	}
	return append(encoded, '\n')
}
