// Copyright 2019+ Klaus Post. All rights reserved.
// License information can be found in the LICENSE file.
// Based on work by Yann Collet, released under BSD License.

package zstd

import (
	"fmt"
	rdebug "runtime/debug"
	"sync"
)

type encJob struct {
	prefix    []byte        // overlap from previous job (nil for first)
	input     []byte        // job's own input data (swapped from filling)
	last      bool          // last block of last job gets last=true
	output    []byte        // compressed blocks (filled by worker)
	err       error         // encoding error
	done      chan struct{} // 1-buffered signal when complete (reusable!)
	inputPtr  *[]byte       // pooled header for input
	outputPtr *[]byte       // pooled header for output
	prefixPtr *[]byte       // pooled header for prefix
}

type jobState struct {
	jobSize     int
	overlapSize int
	filling     []byte  // accumulates input up to jobSize
	fillingPtr  *[]byte // pooled header for filling
	nextPrefix  []byte  // overlap prefix prepared for the next dispatched job
	nextPrefPtr *[]byte // pooled header for nextPrefix

	jobSeq int // next job sequence number

	jobCh    chan *encJob // dispatch to workers
	resultCh chan *encJob // ordered results to flusher

	workerWg  sync.WaitGroup
	flusherWg sync.WaitGroup

	mu         sync.Mutex
	flushedSeq int // last flushed sequence number
	cond       *sync.Cond

	flusherErr error
	started    bool

	jobPool     sync.Pool // *encJob
	inputPool   sync.Pool // *[]byte buffers of jobSize cap
	outputPool  sync.Pool // *[]byte buffers for compressed output
	overlapPool sync.Pool // *[]byte buffers for overlap prefixes
}

func (e *Encoder) startJobWorkers() {
	js := &e.state.jobs
	n := e.o.concurrent
	js.jobCh = make(chan *encJob, n)
	js.resultCh = make(chan *encJob, n)
	js.flushedSeq = 0
	js.cond = sync.NewCond(&js.mu)

	// Workers borrow encoders from the shared e.encoders pool per-job.
	// Ensure the pool is initialized before any worker tries to borrow.
	e.init.Do(e.initialize)

	for range n {
		js.workerWg.Add(1)
		go e.jobWorker()
	}
	js.flusherWg.Add(1)
	go e.jobFlusher()
	js.started = true
}

func (e *Encoder) jobWorker() {
	js := &e.state.jobs
	defer js.workerWg.Done()
	for job := range js.jobCh {
		enc := <-e.encoders
		e.compressJob(enc, job)
		e.encoders <- enc
		job.done <- struct{}{}
	}
}

func (e *Encoder) compressJob(enc encoder, job *encJob) {
	defer func() {
		if r := recover(); r != nil {
			job.err = fmt.Errorf("panic in parallel job: %v", r)
			rdebug.PrintStack()
		}
	}()

	if len(job.prefix) > 0 {
		enc.ResetPrefix(job.prefix)
	} else {
		enc.Reset(nil, false)
	}

	data := job.input
	blk := enc.Block()
	if len(data) == 0 && job.last {
		blk.reset(nil)
		blk.last = true
		oldOut := blk.output
		blk.output = job.output
		blk.encodeRaw(nil)
		job.output = blk.output
		blk.output = oldOut
		return
	}

	// Zero-copy slice path when there is no overlap prefix.
	if len(job.prefix) == 0 {
		if se, ok := enc.(sliceEncoder); ok && useMatchAsm && len(data) < 1<<29 {
			se.PrepareSlice(len(data))
			dst := job.output
			oldOut := blk.output
			for pos := 0; pos < len(data); {
				end := min(pos+e.o.blockSize, len(data))
				todo := data[pos:end]
				blk.pushOffsets()
				se.EncodeSlice(blk, data, pos, end)
				blk.last = end == len(data) && job.last
				blk.output = dst
				err := blk.encode(todo, e.o.noEntropy, !e.o.allLitEntropy)
				dst = blk.output
				blk.output = oldOut
				if err != nil {
					se.FinishSlice(len(data))
					job.output = dst
					job.err = err
					return
				}
				blk.reset(nil)
				pos = end
			}
			se.FinishSlice(len(data))
			job.output = dst
			return
		}
	}

	dst := job.output
	oldOut := blk.output
	for len(data) > 0 {
		todo := data
		if len(todo) > e.o.blockSize {
			todo = todo[:e.o.blockSize]
		}
		data = data[len(todo):]

		blk.pushOffsets()
		enc.Encode(blk, todo)
		blk.last = len(data) == 0 && job.last

		blk.output = dst
		err := blk.encode(todo, e.o.noEntropy, !e.o.allLitEntropy)
		dst = blk.output
		blk.output = oldOut
		if err != nil {
			job.output = dst
			job.err = err
			return
		}
		blk.reset(nil)
	}
	job.output = dst
}

func (js *jobState) getInputBuf(size int) ([]byte, *[]byte) {
	if v := js.inputPool.Get(); v != nil {
		bp := v.(*[]byte)
		b := *bp
		if cap(b) >= size {
			return b[:0], bp
		}
	}
	b := make([]byte, 0, size)
	return b, &b
}

func (js *jobState) putInputBuf(b []byte, bp *[]byte) {
	if cap(b) > 0 {
		if bp == nil {
			bp = new([]byte)
		}
		*bp = b[:0]
		js.inputPool.Put(bp)
	}
}

func (js *jobState) getOutputBuf(size int) ([]byte, *[]byte) {
	if v := js.outputPool.Get(); v != nil {
		bp := v.(*[]byte)
		b := *bp
		if cap(b) >= size {
			return b[:0], bp
		}
	}
	b := make([]byte, 0, size)
	return b, &b
}

func (js *jobState) putOutputBuf(b []byte, bp *[]byte) {
	if cap(b) > 0 {
		if bp == nil {
			bp = new([]byte)
		}
		*bp = b[:0]
		js.outputPool.Put(bp)
	}
}

func (js *jobState) getOverlapBuf(size int) ([]byte, *[]byte) {
	if v := js.overlapPool.Get(); v != nil {
		bp := v.(*[]byte)
		b := *bp
		if cap(b) >= size {
			return b[:size], bp
		}
	}
	b := make([]byte, size)
	return b, &b
}

func (js *jobState) putOverlapBuf(b []byte, bp *[]byte) {
	if cap(b) > 0 {
		if bp == nil {
			bp = new([]byte)
		}
		*bp = b[:0]
		js.overlapPool.Put(bp)
	}
}

func (js *jobState) getJob() *encJob {
	if v := js.jobPool.Get(); v != nil {
		return v.(*encJob)
	}
	return &encJob{done: make(chan struct{}, 1)}
}

func (js *jobState) putJob(job *encJob) {
	job.prefix = nil
	job.input = nil
	job.output = nil
	job.inputPtr = nil
	job.outputPtr = nil
	job.prefixPtr = nil
	job.err = nil
	job.last = false
	js.jobPool.Put(job)
}

func (e *Encoder) jobFlusher() {
	js := &e.state.jobs
	defer js.flusherWg.Done()
	for job := range js.resultCh {
		<-job.done
		// Worker has fully exited compressJob, so the prefix is no longer
		// in use. Return it to the pool regardless of outcome.
		if job.prefix != nil {
			js.putOverlapBuf(job.prefix, job.prefixPtr)
			job.prefix = nil
			job.prefixPtr = nil
		}
		if job.err != nil {
			err := job.err
			js.putInputBuf(job.input, job.inputPtr)
			js.putOutputBuf(job.output, job.outputPtr)
			js.putJob(job)
			js.mu.Lock()
			js.flusherErr = err
			js.cond.Broadcast()
			js.mu.Unlock()
			for j := range js.resultCh {
				<-j.done
			}
			return
		}
		if len(job.output) > 0 {
			_, err := e.state.w.Write(job.output)
			if err != nil {
				js.putInputBuf(job.input, job.inputPtr)
				js.putOutputBuf(job.output, job.outputPtr)
				js.putJob(job)
				js.mu.Lock()
				js.flusherErr = err
				js.cond.Broadcast()
				js.mu.Unlock()
				for j := range js.resultCh {
					<-j.done
				}
				return
			}
			e.state.nWritten += int64(len(job.output))
		}
		// Return buffers and job struct to pools.
		js.putInputBuf(job.input, job.inputPtr)
		js.putOutputBuf(job.output, job.outputPtr)
		js.putJob(job)

		js.mu.Lock()
		js.flushedSeq++
		js.cond.Broadcast()
		js.mu.Unlock()
	}
}

func (e *Encoder) shutdownJobWorkers() {
	js := &e.state.jobs
	if !js.started {
		return
	}
	close(js.jobCh)
	js.workerWg.Wait()
	close(js.resultCh)
	js.flusherWg.Wait()
	js.started = false
}

// waitAllJobs blocks until all dispatched jobs have been flushed.
func (e *Encoder) waitAllJobs() {
	js := &e.state.jobs
	if !js.started {
		return
	}
	js.mu.Lock()
	for js.flushedSeq < js.jobSeq && js.flusherErr == nil {
		js.cond.Wait()
	}
	js.mu.Unlock()
}

func (e *Encoder) dispatchJob(final bool) error {
	s := &e.state
	js := &s.jobs

	js.mu.Lock()
	fErr := js.flusherErr
	js.mu.Unlock()
	if fErr != nil {
		return fErr
	}

	if !s.headerWritten {
		// Single-block optimization: fall through to encodeAll path.
		if final && len(js.filling) > 0 && len(js.filling) <= e.o.blockSize {
			s.current = e.encodeAll(s.encoder, js.filling, s.current[:0])
			var n2 int
			n2, s.err = s.w.Write(s.current)
			if s.err != nil {
				return s.err
			}
			s.nWritten += int64(n2)
			s.nInput += int64(len(js.filling))
			s.current = s.current[:0]
			js.filling = js.filling[:0]
			s.headerWritten = true
			s.fullFrameWritten = true
			s.eofWritten = true
			return nil
		}
		if final && len(js.filling) == 0 && !e.o.fullZero {
			s.headerWritten = true
			s.fullFrameWritten = true
			s.eofWritten = true
			return nil
		}

		var tmp [maxHeaderSize]byte
		fh := frameHeader{
			ContentSize:   uint64(s.frameContentSize),
			WindowSize:    uint32(s.encoder.WindowSize(s.frameContentSize)),
			SingleSegment: false,
			Checksum:      e.o.crc,
			DictID:        0,
		}
		dst := fh.appendTo(tmp[:0])
		var n2 int
		n2, s.err = s.w.Write(dst)
		if s.err != nil {
			return s.err
		}
		s.nWritten += int64(n2)
		s.headerWritten = true
	}

	if len(js.filling) == 0 && !final {
		return nil
	}

	if !js.started {
		e.startJobWorkers()
	}

	// Size output buffer to worst-case raw block size (3-byte header per block) so compressJob never reallocates.
	outputEst := len(js.filling) + (len(js.filling)/e.o.blockSize+1)*3 + 64
	outBuf, outPtr := js.getOutputBuf(outputEst)

	job := js.getJob()
	job.last = final
	job.output = outBuf
	job.outputPtr = outPtr

	// Each job owns its prefix slice; the flusher returns it to the pool
	// after <-job.done, so workers and dispatch never share a buffer.
	if js.nextPrefix != nil {
		job.prefix = js.nextPrefix
		job.prefixPtr = js.nextPrefPtr
		js.nextPrefix = nil
		js.nextPrefPtr = nil
	}

	// Build the next job's prefix from the tail of this job's input.
	if !final && len(js.filling) > 0 {
		overlapLen := min(js.overlapSize, len(js.filling))
		np, npPtr := js.getOverlapBuf(overlapLen)
		copy(np, js.filling[len(js.filling)-overlapLen:])
		js.nextPrefix = np
		js.nextPrefPtr = npPtr
	}

	// Swap filling buffer into job — zero-copy for the input data.
	job.input = js.filling
	job.inputPtr = js.fillingPtr
	js.filling, js.fillingPtr = js.getInputBuf(js.jobSize)

	s.nInput += int64(len(job.input))
	js.jobSeq++

	if final {
		s.eofWritten = true
	}

	js.resultCh <- job
	js.jobCh <- job

	return nil
}
