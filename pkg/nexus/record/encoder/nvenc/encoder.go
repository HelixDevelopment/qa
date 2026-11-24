// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

// Package nvenc is the OCU P5.5 NVIDIA NVENC encoder backend.
// It routes H.264/HEVC encode requests to a KindNVENC Worker on
// host-a.example.com via ocuremote.Dispatcher, reusing the SSH trust
// established in P2 — no new credential or firewall rule required.
//
// The real gRPC server on host-a.example.com is P5.6 operator scope; P5.5
// wires the client side so that once the sidecar is running the encoder
// Just Works. When no GPU host satisfies the capability the encoder
// degrades gracefully to ErrNotWired.
//
// Proto schema: see proto/nvenc.proto for the full P5.6 request/response
// definition. P5.5 passes *structpb.Value as a proto.Message placeholder
// because the sidecar gRPC server is not yet compiled; P5.6 replaces the
// placeholder with the generated NVENCRequest / NVENCResponse types.
//
// Kill-switch: HELIXQA_RECORD_NVENC_STUB=1 forces ErrNotWired without
// consulting the Dispatcher, useful for CI environments without a GPU host.
package nvenc

import (
	"context"
	"fmt"
	"os"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	contracts "digital.vasic.helixqa/pkg/nexus/native/contracts"
	"digital.vasic.helixqa/pkg/nexus/record/encoder"
)

func init() {
	encoder.Register("nvenc", func() encoder.Encoder {
		return newEncoder()
	})
}

// newEncoder is package-level injectable; tests replace it with a mock.
var newEncoder = func() encoder.Encoder {
	return &productionEncoder{}
}

// productionEncoder is the not-yet-started state returned by the factory.
// Encode() returns ErrNotWired until NewProductionEncoder is used with a
// Dispatcher.
type productionEncoder struct{}

// Encode implements encoder.Encoder. Returns ErrNotWired until
// NewProductionEncoder is used.
func (e *productionEncoder) Encode(_ contracts.Frame) error {
	return encoder.ErrNotWired
}

// Close implements encoder.Encoder.
func (e *productionEncoder) Close() error {
	return nil
}

// ---------------------------------------------------------------------------
// Real encoder — constructed by NewProductionEncoder
// ---------------------------------------------------------------------------

// RecordConfig supplies width, height, and frame rate to the NVENC session.
type RecordConfig struct {
	Width     int
	Height    int
	FrameRate int
}

// liveEncoder holds an active Dispatcher and the lazily-resolved Worker.
type liveEncoder struct {
	cfg        RecordConfig
	dispatcher contracts.Dispatcher
	mu         sync.Mutex
	worker     contracts.Worker // lazily resolved on first Encode
	once       sync.Once
	closed     bool
}

// NewProductionEncoder constructs an NVENC encoder backed by a remote Worker
// resolved via dispatcher.
//
//   - cfg supplies width, height, and frame rate.
//   - dispatcher is the ocuremote.Dispatcher that selects the best GPU host.
//
// Returns ErrNotWired when:
//   - HELIXQA_RECORD_NVENC_STUB=1 is set.
//   - dispatcher is nil.
//
// The out io.Writer parameter is reserved for P5.6 (the sidecar will stream
// encoded H.264 back via a side channel); pass nil in P5.5.
func NewProductionEncoder(cfg RecordConfig, dispatcher contracts.Dispatcher) (encoder.Encoder, error) {
	if stubActive() {
		return nil, encoder.ErrNotWired
	}
	if dispatcher == nil {
		return nil, encoder.ErrNotWired
	}
	return &liveEncoder{
		cfg:        cfg,
		dispatcher: dispatcher,
	}, nil
}

// Encode sends one frame to the remote NVENC worker.
// On the first call it lazily resolves the Worker via Dispatcher.Resolve.
// If Resolve fails (no GPU host satisfies KindNVENC) ErrNotWired is returned.
//
// P5.5 sends a *structpb.Value placeholder as the proto.Message request.
// P5.6 replaces it with the generated NVENCRequest from proto/nvenc.proto,
// carrying frame.Seq, frame.Width, frame.Height, and the raw BGRA bytes.
func (e *liveEncoder) Encode(frame contracts.Frame) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return fmt.Errorf("nvenc: encoder already closed")
	}

	// Lazy-resolve the remote Worker on first Encode call.
	if e.worker == nil {
		w, err := e.dispatcher.Resolve(context.Background(), contracts.Capability{
			Kind:    contracts.KindNVENC,
			MinVRAM: 1024,
		})
		if err != nil {
			return encoder.ErrNotWired
		}
		e.worker = w
	}

	// P5.5 placeholder: send a structpb.Value carrying the frame sequence
	// number as a float64. P5.6 replaces this with a fully-typed NVENCRequest
	// carrying the pixel data. See proto/nvenc.proto for the target schema.
	req := structpb.NewNumberValue(float64(frame.Seq))
	resp := new(structpb.Value)
	if err := e.worker.Call(context.Background(), req, resp); err != nil {
		return fmt.Errorf("nvenc: worker call: %w", err)
	}
	return nil
}

// Close releases the remote Worker.
func (e *liveEncoder) Close() error {
	var closeErr error
	e.once.Do(func() {
		e.mu.Lock()
		e.closed = true
		w := e.worker
		e.worker = nil
		e.mu.Unlock()

		if w != nil {
			if err := w.Close(); err != nil {
				closeErr = fmt.Errorf("nvenc: close worker: %w", err)
			}
		}
	})
	return closeErr
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func stubActive() bool {
	return os.Getenv("HELIXQA_RECORD_NVENC_STUB") == "1"
}
