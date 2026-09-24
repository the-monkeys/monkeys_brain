package services

import (
	"reflect"
	"testing"
)

func TestGroupDeleteRoutingKeyIndexes(t *testing.T) {
	if got := groupDeleteRoutingKeyIndexes(0); len(got) != 0 {
		t.Fatalf("no routing keys: %v", got)
	}
	if got := groupDeleteRoutingKeyIndexes(1); !reflect.DeepEqual(got, []int{storageRoutingKey}) {
		t.Fatalf("storage only: %v", got)
	}
	if got := groupDeleteRoutingKeyIndexes(3); !reflect.DeepEqual(got, []int{storageRoutingKey}) {
		t.Fatalf("blog key missing until index 3 exists: %v", got)
	}
	if got := groupDeleteRoutingKeyIndexes(4); !reflect.DeepEqual(got, []int{storageRoutingKey, blogRoutingKey}) {
		t.Fatalf("storage+blog: %v", got)
	}
}

func TestGroupAudienceCoerceRoutingKeyIndexes(t *testing.T) {
	if got := groupAudienceCoerceRoutingKeyIndexes(0); len(got) != 0 {
		t.Fatalf("no keys: %v", got)
	}
	if got := groupAudienceCoerceRoutingKeyIndexes(2); !reflect.DeepEqual(got, []int{usersRoutingKey}) {
		t.Fatalf("users only: %v", got)
	}
	if got := groupAudienceCoerceRoutingKeyIndexes(4); !reflect.DeepEqual(got, []int{usersRoutingKey, blogRoutingKey}) {
		t.Fatalf("users+blog: %v", got)
	}
}
