package bridge

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func ExtractEnvFileArg(args []string) (string, []string, error) {
	filtered := make([]string, 0, len(args))
	var envFile string
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--env-file":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("--env-file requires a path")
			}
			envFile = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(arg, "--env-file="):
			envFile = strings.TrimSpace(strings.TrimPrefix(arg, "--env-file="))
		default:
			filtered = append(filtered, args[i])
		}
	}
	return envFile, filtered, nil
}

func LoadEnvFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid env assignment at line %d", lineNumber)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return fmt.Errorf("empty env key at line %d", lineNumber)
		}
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return scanner.Err()
}
