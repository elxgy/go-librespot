package mercury

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	librespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/ap"
	spotifypb "github.com/elxgy/go-librespot/proto/spotify"
	"google.golang.org/protobuf/proto"
	"sync"
	"time"
)

type hermesRequest struct {
	header *spotifypb.MercuryHeader
	parts  [][]byte
	seq    uint64

	resp chan hermesResponse
}

type hermesResponse struct {
	header *spotifypb.MercuryHeader
	parts  [][]byte

	err error
}

type Client struct {
	log     librespot.Logger
	ap      *ap.Accesspoint
	baseCtx context.Context

	recvLoopOnce sync.Once

	reqsMu  sync.Mutex
	reqs    map[uint64]hermesRequest
	nextSeq uint64

	reqChan chan hermesRequest
}

func NewClient(log librespot.Logger, accesspoint *ap.Accesspoint, baseCtx context.Context) *Client {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	c := &Client{log: log, ap: accesspoint, baseCtx: baseCtx, reqs: make(map[uint64]hermesRequest)}
	c.reqChan = make(chan hermesRequest)
	return c
}

func (c *Client) startReceiving() {
	c.recvLoopOnce.Do(func() { go c.recvLoop() })
}

func (c *Client) recvLoop() {
	ch := c.ap.Receive(ap.PacketTypeMercuryReq, ap.PacketTypeMercurySub, ap.PacketTypeMercuryUnsub, ap.PacketTypeMercuryEvent)
	done := c.ap.Done()

	for {
		select {
		case <-done:
			c.failPendingRequests(ap.ErrAccesspointClosed)
			return
		case pkt, ok := <-ch:
			if !ok {
				return
			}

			if pkt.Type != ap.PacketTypeMercuryReq {
				c.log.Warnf("skipping mercury packet with type: %s", pkt.Type.String())
				continue
			}

			resp := bytes.NewReader(pkt.Payload)

			var seqLen uint16
			_ = binary.Read(resp, binary.BigEndian, &seqLen)

			var respSeq uint64
			switch seqLen {
			case 8:
				_ = binary.Read(resp, binary.BigEndian, &respSeq)
			case 4:
				var seq32 uint32
				_ = binary.Read(resp, binary.BigEndian, &seq32)
				respSeq = uint64(seq32)
			case 2:
				var seq16 uint16
				_ = binary.Read(resp, binary.BigEndian, &seq16)
				respSeq = uint64(seq16)
			default:
				c.log.Warnf("received mercury response with invalid sequence length: %d", seqLen)
				continue
			}

			var flags uint8
			_ = binary.Read(resp, binary.BigEndian, &flags)

			if flags != 1 {
				c.log.Warnf("received unsupported partial mercury response: %d", flags)
				continue
			}

			var partsCount uint16
			_ = binary.Read(resp, binary.BigEndian, &partsCount)

			req, ok := c.takeRequest(respSeq)
			if !ok {
				c.log.Warnf("received mercury response with invalid sequence: %d", respSeq)
				continue
			}

			parts := make([][]byte, partsCount)
			for i := uint16(0); i < partsCount; i++ {
				var partLen uint16
				_ = binary.Read(resp, binary.BigEndian, &partLen)

				part := make([]byte, partLen)
				_, _ = resp.Read(part)
				parts[i] = part
			}

			if len(parts) == 0 {
				req.resp <- hermesResponse{err: fmt.Errorf("received empty mercury response")}
				continue
			}

			var header spotifypb.MercuryHeader
			if err := proto.Unmarshal(parts[0], &header); err != nil {
				req.resp <- hermesResponse{err: fmt.Errorf("failed unmarshaling mercury header: %w", err)}
				continue
			}

			req.resp <- hermesResponse{header: &header, parts: parts[1:]}
		case req := <-c.reqChan:
			c.reqChanReq(req)
		}
	}
}

func (c *Client) reqChanReq(req hermesRequest) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(8))                // sequence length
	_ = binary.Write(&buf, binary.BigEndian, req.seq)                  // sequence
	_ = binary.Write(&buf, binary.BigEndian, uint8(1))                 // flags
	_ = binary.Write(&buf, binary.BigEndian, uint16(1+len(req.parts))) // parts count

	headerBytes, err := proto.Marshal(req.header)
	if err != nil {
		c.takeRequest(req.seq)
		req.resp <- hermesResponse{err: fmt.Errorf("failed marshaling mercury header: %w", err)}
		return
	}

	_ = binary.Write(&buf, binary.BigEndian, uint16(len(headerBytes)))
	_, _ = buf.Write(headerBytes)

	for _, part := range req.parts {
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(part)))
		_, _ = buf.Write(part)
	}

	if err := c.ap.Send(c.baseCtx, ap.PacketTypeMercuryReq, buf.Bytes()); err != nil {
		c.takeRequest(req.seq)
		req.resp <- hermesResponse{err: fmt.Errorf("failed sending mercury request: %w", err)}
	}
}

func (c *Client) putRequest(req hermesRequest) {
	c.reqsMu.Lock()
	c.reqs[req.seq] = req
	c.reqsMu.Unlock()
}

func (c *Client) takeRequest(seq uint64) (hermesRequest, bool) {
	c.reqsMu.Lock()
	defer c.reqsMu.Unlock()
	req, ok := c.reqs[seq]
	if ok {
		delete(c.reqs, seq)
	}
	return req, ok
}

func (c *Client) failPendingRequests(err error) {
	c.reqsMu.Lock()
	pending := make([]hermesRequest, 0, len(c.reqs))
	for seq, req := range c.reqs {
		pending = append(pending, req)
		delete(c.reqs, seq)
	}
	c.reqsMu.Unlock()

	for _, req := range pending {
		req.resp <- hermesResponse{err: err}
	}
}

func (c *Client) Request(ctx context.Context, method, uri string, fields map[string][]byte, payload []byte) ([]byte, error) {
	done := c.ap.Done()

	c.startReceiving()

	header := &spotifypb.MercuryHeader{
		Method: proto.String(method),
		Uri:    proto.String(uri),
	}

	if fields != nil {
		for k, v := range fields {
			header.UserFields = append(header.UserFields, &spotifypb.MercuryUserField{
				Key: proto.String(k), Value: v,
			})
		}
	}

	var parts [][]byte
	for i := 0; i < len(payload); i += 0xffff {
		parts = append(parts, payload[i:min(len(payload), i+0xffff)])
	}

	req := hermesRequest{resp: make(chan hermesResponse, 1)}
	c.reqsMu.Lock()
	req.seq = c.nextSeq
	c.nextSeq++
	c.reqsMu.Unlock()
	req.header = header
	req.parts = parts

	c.putRequest(req)
	defer c.takeRequest(req.seq)

	select {
	case <-done:
		return nil, ap.ErrAccesspointClosed
	case c.reqChan <- req:
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var resp hermesResponse
	select {
	case resp = <-req.resp:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
		return nil, ap.ErrAccesspointClosed
	}

	if resp.err != nil {
		return nil, resp.err
	}

	if *resp.header.StatusCode != 200 {
		return nil, fmt.Errorf("mercury request failed with status code: %d", *resp.header.StatusCode)
	}

	var respPayload []byte
	for _, part := range resp.parts {
		respPayload = append(respPayload, part...)
	}

	return respPayload, nil
}
