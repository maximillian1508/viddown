package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

const maxSingleHeaderBytes = 2048

// sanitizeHeaders keeps only short headers ffmpeg needs.
// Never pass fat Cookie jars — they cause "argument list too long".
// These CDNs usually auth via URL query (auth_key=…) + Referer.
func sanitizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := headers[k]; v != "" {
				return v
			}
			if v := headers[strings.ToLower(k)]; v != "" {
				return v
			}
		}
		return ""
	}

	out := make(map[string]string)
	if v := get("User-Agent", "user-agent"); v != "" && len(v) <= maxSingleHeaderBytes {
		out["User-Agent"] = v
	}
	if v := get("Referer", "referer"); v != "" && len(v) <= maxSingleHeaderBytes {
		out["Referer"] = v
	}
	if v := get("Origin", "origin"); v != "" && len(v) <= maxSingleHeaderBytes {
		out["Origin"] = v
	}
	// Intentionally omit Cookie — page cookie jars blow ARG_MAX on this host.
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildHeadersArg(headers map[string]string) string {
	headers = sanitizeHeaders(headers)
	if len(headers) == 0 {
		return ""
	}
	var b strings.Builder
	order := []string{"User-Agent", "Referer", "Origin"}
	for _, k := range order {
		v := headers[k]
		if v == "" {
			continue
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	return b.String()
}

// appendInputArgs adds network timeouts, optional headers, and -i URL.
// Do NOT use -user_agent: Debian ffmpeg 5.1 errors with "Option user_agent not found"
// and then mis-parses the rest of the command (Invalid data found…).
func appendInputArgs(args []string, headers map[string]string, inputURL string) []string {
	args = append(args,
		"-rw_timeout", "15000000",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
	)
	if hdr := buildHeadersArg(headers); hdr != "" {
		args = append(args, "-headers", hdr)
	}
	return append(args, "-i", inputURL)
}

func ffmpegCommand(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

func ffprobeCommand(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "ffprobe", args...)
}

// appendOutputMetadata tags the output MP4 (visible in ffprobe, VLC, Filebrowser).
func appendOutputMetadata(args []string, title, sourceURL string) []string {
	if title != "" {
		args = append(args, "-metadata", "title="+title)
	}
	if sourceURL != "" {
		args = append(args, "-metadata", "comment=Source: "+sourceURL)
		args = append(args, "-metadata", "description="+sourceURL)
	}
	return args
}

func writeHeadersFile(dir string, headers map[string]string) (string, error) {
	arg := buildHeadersArg(headers)
	if arg == "" {
		return "", nil
	}
	f, err := os.CreateTemp(dir, "headers-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(arg); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
