package plugintarget

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrPrompt reports invalid or unavailable interactive input.
var ErrPrompt = errors.New("select plugin interactively")

// Prompt returns a deterministic numeric selector for an available terminal.
func Prompt(input io.Reader, output io.Writer) Selector {
	return func(candidates []Target) (int, error) {
		if input == nil || output == nil {
			return -1, fmt.Errorf("%w: input and output are required", ErrPrompt)
		}
		if len(candidates) == 0 {
			return -1, fmt.Errorf("%w: no candidates", ErrPrompt)
		}
		if _, err := io.WriteString(output, "Multiple local plugins:\n"); err != nil {
			return -1, fmt.Errorf("%w: write choices: %v", ErrPrompt, err)
		}
		for index, candidate := range candidates {
			if _, err := fmt.Fprintf(output, "  %d. %s (%s)\n", index+1, candidate.ID(), candidate.Directory()); err != nil {
				return -1, fmt.Errorf("%w: write choice: %v", ErrPrompt, err)
			}
		}
		if _, err := fmt.Fprintf(output, "Select plugin [1-%d]: ", len(candidates)); err != nil {
			return -1, fmt.Errorf("%w: write prompt: %v", ErrPrompt, err)
		}
		scanner := bufio.NewScanner(input)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return -1, fmt.Errorf("%w: read choice: %v", ErrPrompt, err)
			}
			return -1, fmt.Errorf("%w: input ended before a choice", ErrPrompt)
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) != 1 {
			return -1, fmt.Errorf("%w: enter one number", ErrPrompt)
		}
		choice, err := strconv.Atoi(fields[0])
		if err != nil || choice < 1 || choice > len(candidates) {
			return -1, fmt.Errorf("%w: choice must be between 1 and %d", ErrPrompt, len(candidates))
		}
		return choice - 1, nil
	}
}
