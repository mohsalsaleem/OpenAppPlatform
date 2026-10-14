package controller

import "testing"

func TestQueueFailureClassificationNeverCopiesPrivateErrorContents(t *testing.T) {
	cases := map[string]string{
		"Docker POST /networks/create returned HTTP 400":                      "network_creation_rejected",
		"image is not cached; pre-pull it or configure pullPolicy if-missing": "image_not_cached",
		"deployment read failed: private-secret":                              "deployment_read_failed",
		"private-secret (SQLSTATE 42P01)":                                     "database_42P01",
		"private-secret":                                                      "worker_retry",
	}
	for message, want := range cases {
		if got := queueFailureCode(message); got != want {
			t.Fatalf("classification %q expected %q", got, want)
		}
	}
}
