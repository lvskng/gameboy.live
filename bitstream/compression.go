package bitstream

type pixelCluster struct {
	pixel           byte
	repetitionIndex uint8
	repetitionCount uint8
}

func (s *Server) GetBitmap() ([][]byte, [160][144]byte) {
	s.pixelLock.RLock()
	screen := *s.pixels
	s.pixelLock.RUnlock()
	var compressedBitmap [][]byte
	for linenum, line := range screen {
		lineClusters := getLineClusters(line[:])
		compressedLine := CompressLine(line[:], lineClusters)
		compressedBitmap = append(compressedBitmap, append([]byte{byte(linenum) + 0x04}, compressedLine...))
	}
	return compressedBitmap, screen
}

func (s *Server) GetBitmapDelta(lastBitmap [160][144]byte) ([][]byte, [160][144]byte) {
	s.pixelLock.RLock()
	screen := *s.pixels
	s.pixelLock.RUnlock()
	var deltaScreen [][]byte
	for linenum, line := range screen {
		compressedDeltaLine := []byte{byte(linenum) + 0x04}
		var deltaLine []byte
		lastLine := lastBitmap[linenum]

		if line == lastLine {
			continue
		}
		for index, pixel := range line {
			if pixel == lastLine[index] {
				deltaLine = append(deltaLine, 0xFF)
			} else {
				deltaLine = append(deltaLine, pixel)
			}
		}
		//clustering
		clusters := getLineClusters(deltaLine)
		compressedDeltaLine = append(compressedDeltaLine, CompressLine(deltaLine, clusters)...)

		deltaScreen = append(deltaScreen, compressedDeltaLine)
	}
	return deltaScreen, screen
}

// get repeating sections of a line for compression
func getLineClusters(line []byte) []pixelCluster {
	var clusters []pixelCluster
	var lastPixel byte
	var repetitionCount uint8
	var repetitionIndex uint8
	for index, pixel := range line {
		if index == 0 {
			lastPixel = pixel
			continue
		}
		if repetitionCount > 0 {
			if lastPixel == pixel {
				repetitionCount++
				if index == len(line)-1 {
					if repetitionCount > 1 {
						clusters = append(clusters, pixelCluster{lastPixel, repetitionIndex, repetitionCount})
					}
				}
			} else {
				if repetitionCount > 1 {
					clusters = append(clusters, pixelCluster{lastPixel, repetitionIndex, repetitionCount})
				}
				repetitionCount = 0
				lastPixel = pixel
			}
		} else {
			if lastPixel == pixel {
				repetitionIndex = uint8(index - 1) //-1 because the cluster starts at the first equal int
				repetitionCount = 1
			} else {
				lastPixel = pixel
			}
		}
	}
	return clusters
}

func shiftPixelCluster(clusters []pixelCluster) (pixelCluster, []pixelCluster) {
	c := clusters[0]
	return c, clusters[1:]
}

// Compresses a display line by eliminating clusters with a repeat declaration (RLE)
// 0x00 - 0x03 regular pixel color value
// 0xFF no change in pixel value since last image
// 0x04 - 0xA3 line identifier
// 0xDx repeat the following byte x times
// 0xF0 start regular line without compression
// 0xF1 start compressed line
// 0xF2 0xXX repeat the following byte XX times
// 0xFD repeat until end of line
// 0xFE ignore line
// 0xEE internally used to mark array elements that are to be removed
func CompressLine(origLine []byte, cl []pixelCluster) []byte {
	if len(cl) == 0 {
		return append([]byte{0xF0}, origLine...)
	}

	line := make([]byte, len(origLine))
	copy(line, origLine)

	var cline []byte

	cline = make([]byte, 1, len(line)/2)
	cline[0] = 0xF1

	var cluster pixelCluster
	clusters := &cl
	hasClusters := len(*clusters) > 0
	if !hasClusters {
		return append([]byte{0xF0}, line...)
	}
	for len(*clusters) > 0 {
		cluster, clusters = shift(clusters)
		i := cluster.repetitionIndex
		clusterEnd := i + cluster.repetitionCount
		if clusterEnd >= 143 {
			line[i] = 0xFD
			i += 2
		} else if cluster.repetitionCount < 16 {
			line[i] = 0xD0 + cluster.repetitionCount
			i += 2
		} else {
			line[i] = 0xF2
			i++
			line[i] = cluster.repetitionCount
			i += 2
		}
		for ; i <= clusterEnd; i++ {
			line[i] = 0xEE //trim
		}
		cline = []byte{0xF1}
	}
	for _, px := range line {
		if px != 0xEE {
			cline = append(cline, px)
		}
	}

	return cline
}

// dataStream is a struct representing the data to be decompressed
// It implements methods like shift() and get() for convenience
type dataStream struct {
	data        []byte
	index, x, y int
}

// shift increases the current data index by 1 and returns the next element
func (s *dataStream) shift() byte {
	b := s.data[s.index]
	s.index = s.index + 1
	return b
}

// get returns the next element without increasing the index
func (s *dataStream) get() byte {
	return s.data[s.index]
}

func (s *dataStream) getNext() byte {
	return s.data[s.index+1]
}

// hasNext returns true if the index is within the bounds of the data slice
func (s *dataStream) hasNext() bool {
	return s.index < len(s.data)
}

func (s *dataStream) xInc() int {
	i := s.x
	s.x++
	return i
}

func (s *dataStream) yInc() int {
	i := s.y
	s.y++
	return i
}

func Decompress(data *[]byte, drawFunc func(pixel byte, x, y int)) {
	s := dataStream{*data, 0, 0, 0}
	s.shift() //Remove mode identifier
	for s.hasNext() {
		operation := s.shift()
		if operation > 0x03 && operation < 0xA5 {
			s.x = int(operation - 4)
			s.y = 0
		} else if operation == 0xF0 {
			drawUncompressed(&s, drawFunc)
		} else if operation == 0xF1 {
			drawCompressed(&s, drawFunc)
		}
	}
}

func drawUncompressed(s *dataStream, drawFunc func(pixel byte, x, y int)) {
	for s.hasNext() {
		peek := s.get()
		if peek > 0x04 && peek < 0xA4 {
			break
		}
		pixel := s.shift()
		if pixel != 0xFF {
			drawFunc(pixel, s.x, s.yInc())
		}
	}
}

func drawCompressed(s *dataStream, drawFunc func(pixel byte, x, y int)) {
	//for next := s.getNext(); s.hasNext() && (next < 0x04 || next == 0xFF); next = s.getNext() {
	for s.hasNext() {
		peek := s.get()
		if peek > 0x04 && peek < 0xA4 {
			break
		}
		b := s.shift()
		if b < 0x04 {
			drawFunc(b, s.x, s.yInc())
		} else if b == 0xFF {
			s.yInc()
		} else if b == 0xF2 {
			repcount := s.shift()
			pixel := s.shift()
			drawRepeat(s, repcount, pixel, drawFunc)
		} else if b > 0xD0 && b < 0xE0 {
			repcount := b - 0xD0
			pixel := s.shift()
			drawRepeat(s, repcount, pixel, drawFunc)
		} else if b == 0xFD {
			repcount := 143 - s.y
			pixel := s.shift()
			drawRepeat(s, byte(repcount), pixel, drawFunc)
		} else {
			panic("help")
		}
	}
}

func drawRepeat(s *dataStream, repcount, pixel byte, drawFunc func(pixel byte, x, y int)) {
	if pixel == 0xFF {
		s.y += int(repcount + 1)
	} else {
		for i := 0; i <= int(repcount); i++ {
			drawFunc(pixel, s.x, s.yInc())
		}
	}
}

func DecompressSimple(data *[]byte, drawFunc func(pixel byte, x, y int)) {
	var b byte
	b, data = shift(data)
	x := 0
	y := 0
	for len(*data) > 0 {
		var op byte
		op, data = shift(data)
		if op > 0x03 && op < 0xA5 {
			x = int(op - 4)
			y = 0
		} else if op == 0xF0 {
			for len(*data) > 0 && ((*data)[0] < 0x04 || (*data)[0] == 0xFF) {
				var pixel byte
				pixel, data = shift(data)
				if pixel != 0xFF {
					drawFunc(pixel, x, y)
				}
				y++
			}
		} else if op == 0xF1 {
			for len(*data) > 0 && ((*data)[0] < 0x04 || (*data)[0] > 0xA4) {
				b, data = shift(data)
				if b < 0x04 {
					drawFunc(b, x, y)
					y++
				} else if b == 0xFF {
					y++
				} else if b == 0xF2 {
					var rcount byte
					var rpx byte
					rcount, data = shift(data)
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							drawFunc(rpx, x, y)
						}
						y++
					}
				} else if b > 0xD0 && b < 0xE0 {
					var rpx byte
					rcount := b - 0xD0
					rpx, data = shift(data)
					for i := 0; i <= int(rcount); i++ {
						if rpx != 0xFF {
							drawFunc(rpx, x, y)
						}
						y++
					}
				} else if b == 0xFD {
					var rpx byte
					rpx, data = shift(data)
					for ; y < 144; y++ {
						if rpx != 0xFF {
							drawFunc(rpx, x, y)
						}
					}
				}
			}
		} else if op == 0xFE {
			continue
		}
	}
}

// shift takes a slice and returns the first element and a pointer to the original slice with this element removed
func shift[T any](slc *[]T) (T, *[]T) {
	var r []T
	if len(*slc) == 1 {
		return (*slc)[0], &r
	}
	r = (*slc)[1:]
	return (*slc)[0], &r
}
