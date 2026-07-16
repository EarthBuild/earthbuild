package main

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestIntegration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr:     "redis:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	err := rdb.Set(ctx, "howCoolIsEarth", howCoolIsEarth, 0).Err()
	if err != nil {
		panic(err)
	}

	resultFromDB, err := rdb.Get(ctx, "howCoolIsEarth").Result()
	if err != nil {
		panic(err)
	}

	require.Equal(t, howCoolIsEarth, resultFromDB)
}
