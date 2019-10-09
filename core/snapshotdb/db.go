package snapshotdb

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/big"
	"path"

	"github.com/PlatONnetwork/PlatON-Go/common"
	"github.com/syndtr/goleveldb/leveldb/journal"
	"github.com/syndtr/goleveldb/leveldb/memdb"
)

func getBaseDBPath(dbpath string) string {
	return path.Join(dbpath, DBBasePath)
}

func (s *snapshotDB) getBlockFromJournal(fd fileDesc) (*blockData, error) {
	reader, err := s.storage.Open(fd)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	journals := journal.NewReader(reader, nil, false, false)
	j, err := journals.Next()
	if err != nil {
		return nil, err
	}
	var header journalHeader
	if err := decode(j, &header); err != nil {
		return nil, err
	}
	block := new(blockData)
	block.ParentHash = header.ParentHash
	block.kvHash = header.KvHash
	if fd.BlockHash != s.getUnRecognizedHash() {
		block.BlockHash = fd.BlockHash
	}
	block.Number = new(big.Int).SetUint64(fd.Num)
	block.data = memdb.New(DefaultComparer, 0)
	block.readOnly = true

	for {
		j, err := journals.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var body journalData
		if err := decode(j, &body); err != nil {
			return nil, err
		}
		if err := block.data.Put(body.Key, body.Value); err != nil {
			return nil, err
		}
		//kvhash = body.Hash
	}
	//block.kvHash = kvhash
	return block, nil
}

func (s *snapshotDB) recover() error {
	//storage
	fds, err := s.storage.List(TypeJournal)
	if err != nil {
		return err
	}
	sortFds(fds)
	baseNum := s.current.BaseNum.Uint64()
	highestNum := s.current.HighestNum.Uint64()
	//read Journal
	if blockchain == nil {
		for _, fd := range fds {
			if baseNum < fd.Num && fd.Num <= highestNum {
				block, err := s.getBlockFromJournal(fd)
				if err != nil {
					return err
				}
				s.committed = append(s.committed, block)
				logger.Debug("recover block ", "num", block.Number, "hash", block.BlockHash.String())
				continue
			}
			if err := s.storage.Remove(fd); err != nil {
				return err
			}
		}
	} else {
		for _, fd := range fds {
			if baseNum < fd.Num && fd.Num <= highestNum {
				if header := blockchain.GetHeaderByHash(fd.BlockHash); header != nil {
					block, err := s.getBlockFromJournal(fd)
					if err != nil {
						return err
					}
					s.committed = append(s.committed, block)
					logger.Debug("recover block with block chain", "num", block.Number, "hash", block.BlockHash.String())
					continue
				}
			}
			if err := s.storage.Remove(fd); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *snapshotDB) generateKVHash(k, v []byte, hash common.Hash) common.Hash {
	var buf bytes.Buffer
	buf.Write(k)
	buf.Write(v)
	buf.Write(hash.Bytes())
	return rlpHash(buf.Bytes())
}

func (s *snapshotDB) getUnRecognizedHash() common.Hash {
	return common.ZeroHash
}

func (s *snapshotDB) put(hash common.Hash, key, value []byte) error {
	s.unCommit.Lock()
	defer s.unCommit.Unlock()
	block, ok := s.unCommit.blocks[hash]
	if !ok {
		return fmt.Errorf("not find the block by hash:%v", hash.String())
	}
	if block.readOnly {
		return errors.New("can't put read only block")
	}
	block.kvHash = s.generateKVHash(key, value, block.kvHash)
	if err := block.data.Put(key, value); err != nil {
		return err
	}
	return nil
}
