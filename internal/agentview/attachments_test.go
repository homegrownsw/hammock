package agentview

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestPlaceAttachments(t *testing.T) {
	video := "/Users/me/Library/Caches/hammock/attachments/20261113-101500-0a1b2c3d4e5f.mp4"
	a, b := &pb.AgentImage{Ref: "img:a"}, &pb.AgentImage{Ref: "img:b"}
	for _, tc := range []struct {
		name, text  string
		imgs        []*pb.AgentImage
		plain, rich string
		refs        []string
	}{
		{"none", "just words", nil, "just words", "", nil},
		{"images in place", "compare [Image #7] with [Image #8] please", []*pb.AgentImage{a, b},
			"compare with please", "compare ￼ with ￼ please", []string{"img:a", "img:b"}},
		{"video path", video + " what's wrong here?", nil,
			"what's wrong here?", "￼ what's wrong here?", []string{"file:" + video}},
		{"mixed order", "[Image #3] then " + video + " end", []*pb.AgentImage{a},
			"then end", "￼ then ￼ end", []string{"img:a", "file:" + video}},
		{"image without mention", "hello", []*pb.AgentImage{a}, "hello", "", []string{"img:a"}},
		{"more mentions than images", "[Image #1] [Image #2] hi", []*pb.AgentImage{a},
			"hi", "￼ [Image #2] hi", []string{"img:a"}},
		{"another folder's file", "/tmp/clip.mp4 look", nil, "/tmp/clip.mp4 look", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plain, rich, all := PlaceAttachments(tc.text, tc.imgs)
			if plain != tc.plain || rich != tc.rich {
				t.Errorf("got %q, %q; want %q, %q", plain, rich, tc.plain, tc.rich)
			}
			var refs []string
			for _, i := range all {
				refs = append(refs, i.Ref)
			}
			if len(refs) != len(tc.refs) {
				t.Fatalf("refs %v, want %v", refs, tc.refs)
			}
			for i := range refs {
				if refs[i] != tc.refs[i] {
					t.Errorf("refs %v, want %v", refs, tc.refs)
				}
			}
		})
	}
	if _, _, all := PlaceAttachments(video, nil); all[0].MediaType != "video/mp4" {
		t.Errorf("media type %q", all[0].MediaType)
	}
}
