package exif

import (
    "bytes"

    "encoding/binary"

    "github.com/dsoprea/go-logging"
)

var (
    ifdLogger = log.NewLogger("exifjpeg.ifd")
)


// IfdTagEnumerator knows how to decode an IFD and all of the tags it
// describes. Note that the IFDs and the actual values floating throughout the
// whole EXIF block, but the IFD itself has just a minor header and a set of
// repeating, statically-sized records. So, the tags (though not their values)
// are fairly simple to enumerate.
type IfdTagEnumerator struct {
    byteOrder binary.ByteOrder
    rawExif []byte
    ifdOffset uint32
    buffer *bytes.Buffer
}

func NewIfdTagEnumerator(rawExif []byte, byteOrder binary.ByteOrder, ifdOffset uint32) (ite *IfdTagEnumerator) {
    ite = &IfdTagEnumerator{
        rawExif: rawExif,
        byteOrder: byteOrder,
        buffer: bytes.NewBuffer(rawExif[ifdOffset:]),
    }

    return ite
}

// getUint16 reads a uint16 and advances both our current and our current
// accumulator (which allows us to know how far to seek to the beginning of the
// next IFD when it's time to jump).
func (ife *IfdTagEnumerator) getUint16() (value uint16, raw []byte, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    raw = make([]byte, 2)

    _, err = ife.buffer.Read(raw)
    log.PanicIf(err)

    if ife.byteOrder == binary.BigEndian {
        value = binary.BigEndian.Uint16(raw)
    } else {
        value = binary.LittleEndian.Uint16(raw)
    }

    return value, raw, nil
}

// getUint32 reads a uint32 and advances both our current and our current
// accumulator (which allows us to know how far to seek to the beginning of the
// next IFD when it's time to jump).
func (ife *IfdTagEnumerator) getUint32() (value uint32, raw []byte, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    raw = make([]byte, 4)

    _, err = ife.buffer.Read(raw)
    log.PanicIf(err)

    if ife.byteOrder == binary.BigEndian {
        value = binary.BigEndian.Uint32(raw)
    } else {
        value = binary.LittleEndian.Uint32(raw)
    }

    return value, raw, nil
}


type IfdEnumerate struct {
    data []byte
    buffer *bytes.Buffer
    byteOrder binary.ByteOrder
    currentOffset uint32
    ifdTopOffset uint32
}

func NewIfdEnumerate(data []byte, byteOrder binary.ByteOrder) *IfdEnumerate {
    return &IfdEnumerate{
        data: data,
        buffer: bytes.NewBuffer(data),
        byteOrder: byteOrder,
        ifdTopOffset: 6,
    }
}

// ValueContext describes all of the parameters required to find and extract
// the actual tag value.
type ValueContext struct {
    UnitCount uint32
    ValueOffset uint32
    RawValueOffset []byte
    RawExif []byte
}

func (ie *IfdEnumerate) getTagEnumerator(ifdOffset uint32) (ite *IfdTagEnumerator) {
    ite = NewIfdTagEnumerator(
            ie.data[ie.ifdTopOffset:],
            ie.byteOrder,
            ifdOffset)

    return ite
}

// TagVisitor is an optional callback that can get hit for every tag we parse
// through. `rawExif` is the byte array startign after the EXIF header (where
// the offsets of all IFDs and values are calculated from).
type TagVisitor func(indexedIfdName string, tagId uint16, tagType TagType, valueContext ValueContext) (err error)


type IfdTagEntry struct {
    TagId uint16
    TagIndex int
    TagType uint16
    UnitCount uint32
    ValueOffset uint32
    RawValueOffset []byte
    IsIfd bool
}


// ParseIfd decodes the IFD block that we're currently sitting on the first
// byte of.
func (ie *IfdEnumerate) ParseIfd(ifdName string, ifdIndex int, ifdOffset uint32, visitor TagVisitor, doDescend bool) (nextIfdOffset uint32, entries []IfdTagEntry, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    ifdLogger.Debugf(nil, "Parsing IFD [%s] (%d) at offset (%04x).", ifdName, ifdIndex, ifdOffset)

    // Return the name of the IFD as its known in our tag-index. We should skip
    // over the current IFD if this is empty (which means we don't recognize/
    // understand the IFD and, therefore, don't know the tags that are valid for
    // it). Note that we could leave ignoring the tags as a responsibility for
    // the visitor, but then it'd be easy for people to integrate that logic and
    // not realize that they needed to specially handle an empty IFD name until
    // they happened upon some obscure media one day and suddenly have issue if
    // they unwittingly write something that breaks in that situation.
    indexedIfdName := IfdName(ifdName, ifdIndex)
    if indexedIfdName == "" {
        ifdLogger.Debugf(nil, "IFD not known and will not be visited: [%s] (%d)", ifdName, ifdIndex)
    }

    ite := ie.getTagEnumerator(ifdOffset)

    tagCount, _, err := ite.getUint16()
    log.PanicIf(err)

    ifdLogger.Debugf(nil, "Current IFD tag-count: (%d)", tagCount)

    entries = make([]IfdTagEntry, tagCount)

    for i := uint16(0); i < tagCount; i++ {
        tagId, _, err := ite.getUint16()
        log.PanicIf(err)

        tagType, _, err := ite.getUint16()
        log.PanicIf(err)

        unitCount, _, err := ite.getUint32()
        log.PanicIf(err)

        valueOffset, rawValueOffset, err := ite.getUint32()
        log.PanicIf(err)

        if visitor != nil && indexedIfdName != "" {
            tt := NewTagType(tagType, ie.byteOrder)

            vc := ValueContext{
                UnitCount: unitCount,
                ValueOffset: valueOffset,
                RawValueOffset: rawValueOffset,
                RawExif: ie.data[ie.ifdTopOffset:],
            }

            err := visitor(indexedIfdName, tagId, tt, vc)
            log.PanicIf(err)
        }

        tag := IfdTagEntry{
            TagId: tagId,
            TagIndex: int(i),
            TagType: tagType,
            UnitCount: unitCount,
            ValueOffset: valueOffset,
            RawValueOffset: rawValueOffset,
        }

        childIfdName, isIfd := IsIfdTag(tagId)
        if isIfd == true {
            tag.IsIfd = true

            if doDescend == true {
                ifdLogger.Debugf(nil, "Descending to IFD [%s].", childIfdName)

                err := ie.Scan(childIfdName, valueOffset, visitor)
                log.PanicIf(err)
            }
        }

        entries[i] = tag
    }

    nextIfdOffset, _, err = ite.getUint32()
    log.PanicIf(err)

    ifdLogger.Debugf(nil, "Next IFD at offset: (%08x)", nextIfdOffset)

    return nextIfdOffset, entries, nil
}

// Scan enumerates the different EXIF blocks (called IFDs).
func (ie *IfdEnumerate) Scan(ifdName string, ifdOffset uint32, visitor TagVisitor) (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = log.Wrap(state.(error))
        }
    }()

    for ifdIndex := 0;; ifdIndex++ {
        nextIfdOffset, _, err := ie.ParseIfd(ifdName, ifdIndex, ifdOffset, visitor, true)
        log.PanicIf(err)

        if nextIfdOffset == 0 {
            break
        }

        ifdOffset = nextIfdOffset
    }

    return nil
}
