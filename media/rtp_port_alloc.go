// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package media

import (
	"fmt"
	"math/bits"
	"sync"
)

// rtpPortAllocator manages RTP port allocation using a bitmap for efficient
// concurrent port assignment. Each bit represents one even-port slot (RTP port);
// the corresponding odd port (RTP+1) is used for RTCP.
//
// Under high concurrency (1000+ goroutines), this avoids the O(n) linear scan
// and repeated ListenUDP syscall failures of the previous offset-based approach.
// Allocation is O(n/64) worst case via round-robin bitmap scan.
type rtpPortAllocator struct {
	mu      sync.Mutex
	bits    []uint64 // 1 = allocated, 0 = free
	start   int      // first port in range (RTPPortStart)
	size    int      // number of even-port slots = (RTPPortEnd - RTPPortStart) / 2
	nextIdx int      // round-robin cursor
}

var (
	portAllocMu    sync.Mutex
	portAllocInst  *rtpPortAllocator
	portAllocStart int
	portAllocEnd   int
)

// getPortAllocator returns the global port allocator, lazily creating it when
// RTPPortStart/RTPPortEnd are configured. Returns nil if no range is set.
func getPortAllocator() *rtpPortAllocator {
	if RTPPortStart <= 0 || RTPPortEnd <= RTPPortStart {
		return nil
	}

	portAllocMu.Lock()
	defer portAllocMu.Unlock()

	if portAllocInst != nil && portAllocStart == RTPPortStart && portAllocEnd == RTPPortEnd {
		return portAllocInst
	}

	size := (RTPPortEnd - RTPPortStart) / 2
	portAllocInst = &rtpPortAllocator{
		bits:  make([]uint64, (size+63)/64),
		start: RTPPortStart,
		size:  size,
	}
	portAllocStart = RTPPortStart
	portAllocEnd = RTPPortEnd
	return portAllocInst
}

// Allocate reserves the next free even port using round-robin scan.
// Returns an error if all ports in the range are in use.
func (a *rtpPortAllocator) Allocate() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := 0; i < a.size; i++ {
		idx := (a.nextIdx + i) % a.size
		word := idx / 64
		bit := uint(idx % 64)
		if a.bits[word]&(1<<bit) == 0 {
			a.bits[word] |= 1 << bit
			a.nextIdx = (idx + 1) % a.size
			return a.start + idx*2, nil
		}
	}
	return 0, fmt.Errorf("no available ports in range %d:%d (all %d slots in use)", a.start, a.start+a.size*2, a.size)
}

// Release marks a port as free. Ports outside the allocator's range are ignored.
func (a *rtpPortAllocator) Release(port int) {
	idx := (port - a.start) / 2
	if idx < 0 || idx >= a.size {
		return
	}
	a.mu.Lock()
	a.bits[idx/64] &^= 1 << uint(idx%64)
	a.mu.Unlock()
}

// InUse returns the number of currently allocated port slots.
func (a *rtpPortAllocator) InUse() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	count := 0
	for _, w := range a.bits {
		count += bits.OnesCount64(w)
	}
	return count
}
