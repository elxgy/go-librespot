package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"

	librespot "github.com/elxgy/go-librespot"
	"github.com/elxgy/go-librespot/ap"
)

type KeyProviderError struct {
	Code uint16
}

func (e KeyProviderError) Error() string {
	return fmt.Sprintf("failed retrieving aes key with code %d", e.Code)
}

type KeyProvider struct {
	ap      *ap.Accesspoint
	log     librespot.Logger
	baseCtx context.Context

	recvLoopOnce sync.Once

	reqChan chan keyRequest

	reqsMu  sync.Mutex
	reqs    map[uint32]keyRequest
	nextSeq uint32
}

type keyRequest struct {
	seq    uint32
	gid    []byte
	fileId []byte
	resp   chan keyResponse
}

type keyResponse struct {
	key []byte
	err error
}

func NewAudioKeyProvider(log librespot.Logger, ap *ap.Accesspoint, baseCtx context.Context) *KeyProvider {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	p := &KeyProvider{log: log, ap: ap, baseCtx: baseCtx, reqs: make(map[uint32]keyRequest)}
	p.reqChan = make(chan keyRequest)
	return p
}

func (p *KeyProvider) startReceiving() {
	p.recvLoopOnce.Do(func() { go p.recvLoop() })
}

func (p *KeyProvider) recvLoop() {
	ch := p.ap.Receive(ap.PacketTypeAesKey, ap.PacketTypeAesKeyError)
	done := p.ap.Done()

	for {
		select {
		case <-done:
			p.failPendingRequests(ap.ErrAccesspointClosed)
			return
		case pkt, ok := <-ch:
			if !ok {
				return
			}

			resp := bytes.NewReader(pkt.Payload)
			var respSeq uint32
			_ = binary.Read(resp, binary.BigEndian, &respSeq)

			req, ok := p.takeRequest(respSeq)
			if !ok {
				p.log.Warnf("received aes key with invalid sequence: %d", respSeq)
				continue
			}

			switch pkt.Type {
			case ap.PacketTypeAesKey:
				key := make([]byte, 16)
				if _, err := io.ReadFull(resp, key); err != nil {
					req.resp <- keyResponse{err: fmt.Errorf("malformed aes key response for sequence %d: %w", respSeq, err)}
					continue
				}
				req.resp <- keyResponse{key: key}
			case ap.PacketTypeAesKeyError:
				var errCode uint16
				_ = binary.Read(resp, binary.BigEndian, &errCode)
				req.resp <- keyResponse{err: &KeyProviderError{errCode}}
			default:
				p.log.Warnf("unexpected aes key packet type: %s", pkt.Type.String())
			}
		case req := <-p.reqChan:
			var buf bytes.Buffer
			_, _ = buf.Write(req.fileId)
			_, _ = buf.Write(req.gid)
			_ = binary.Write(&buf, binary.BigEndian, req.seq)
			_ = binary.Write(&buf, binary.BigEndian, uint16(0))

			if err := p.ap.Send(p.baseCtx, ap.PacketTypeRequestKey, buf.Bytes()); err != nil {
				p.takeRequest(req.seq)
				req.resp <- keyResponse{err: fmt.Errorf("failed sending key request for file %s, gid: %s: %w",
					hex.EncodeToString(req.fileId), librespot.GidToBase62(req.gid), err)}
				continue
			}

			p.log.Debugf("requested aes key for file %s, gid: %s", hex.EncodeToString(req.fileId), librespot.GidToBase62(req.gid))
		}
	}
}

func (p *KeyProvider) Request(ctx context.Context, gid []byte, fileId []byte) ([]byte, error) {
	done := p.ap.Done()

	p.startReceiving()

	req := keyRequest{gid: gid, fileId: fileId, resp: make(chan keyResponse, 1)}
	p.reqsMu.Lock()
	req.seq = p.nextSeq
	p.nextSeq++
	p.reqsMu.Unlock()

	p.putRequest(req)
	defer p.takeRequest(req.seq)

	select {
	case <-done:
		return nil, ap.ErrAccesspointClosed
	case p.reqChan <- req:
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var resp keyResponse
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

	return resp.key, nil
}

func (p *KeyProvider) putRequest(req keyRequest) {
	p.reqsMu.Lock()
	p.reqs[req.seq] = req
	p.reqsMu.Unlock()
}

func (p *KeyProvider) takeRequest(seq uint32) (keyRequest, bool) {
	p.reqsMu.Lock()
	defer p.reqsMu.Unlock()
	req, ok := p.reqs[seq]
	if ok {
		delete(p.reqs, seq)
	}
	return req, ok
}

func (p *KeyProvider) failPendingRequests(err error) {
	p.reqsMu.Lock()
	pending := make([]keyRequest, 0, len(p.reqs))
	for seq, req := range p.reqs {
		pending = append(pending, req)
		delete(p.reqs, seq)
	}
	p.reqsMu.Unlock()

	for _, req := range pending {
		req.resp <- keyResponse{err: err}
	}
}
