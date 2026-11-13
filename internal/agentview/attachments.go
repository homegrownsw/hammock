package agentview

import (
	"mime"
	"path/filepath"
	"regexp"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// AttachMark is where an attachment sits in a message's rich text (and in
// the composer's text as it's sent).
const AttachMark = "￼"

// attachedHere finds what stands for an attachment in a message you sent:
// Claude Code's "[Image #n]" for a pasted picture, or the path of a file
// the agent view attached (saved by the daemon into hammock/attachments,
// named <date>-<time>-<random>.<ext>: a video, or a picture an agent that
// doesn't take them was sent).
var attachedHere = regexp.MustCompile(`\[Image #\d+\]|/[^\s"'<>]*/hammock/attachments/\d{8}-\d{6}-[0-9a-f]{12}\.[A-Za-z0-9]+`)

// markGap is a mark with the space either side of it, for the plain text.
var markGap = regexp.MustCompile(` ?` + AttachMark + ` ?`)

// PlaceAttachments finds where a sent message's attachments were: images
// (its pictures, in order, matched to its "[Image #n]"s) and files the agent
// view attached by path. It returns the text without them (as clients from
// before show it), the text with AttachMark at each (rich, "" when there
// are none) and every attachment, in order: those placed first, numbered
// by where they sit, then any pictures with no place.
func PlaceAttachments(text string, imgs []*pb.AgentImage) (plain, rich string, all []*pb.AgentImage) {
	next := 0
	placed := 0
	rich = attachedHere.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasPrefix(m, "[Image #") {
			if next >= len(imgs) {
				return m
			}
			all = append(all, imgs[next])
			next++
		} else {
			all = append(all, &pb.AgentImage{Ref: "file:" + m, MediaType: attachmentType(m)})
		}
		placed++
		return AttachMark
	})
	all = append(all, imgs[next:]...)
	if placed == 0 {
		if len(imgs) > 0 {
			text = imageRef.ReplaceAllString(text, "")
		}
		return strings.TrimSpace(text), "", all
	}
	rich = strings.TrimSpace(rich)
	plain = strings.TrimSpace(imageRef.ReplaceAllString(markGap.ReplaceAllString(rich, " "), ""))
	return plain, rich, all
}

// attachmentType is a file's media type by its name.
func attachmentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return strings.SplitN(t, ";", 2)[0]
	}
	return "application/octet-stream"
}
