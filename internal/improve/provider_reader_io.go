package improve

import (
	"io"
	"os"
)

const providerMaxReadBytes = 1 << 20

func readProviderFile(path string) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, providerMaxReadBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) <= providerMaxReadBytes {
		return data, false, nil
	}
	return data[:providerMaxReadBytes], true, nil
}
