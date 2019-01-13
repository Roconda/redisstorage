package redisstorage

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis"
)

// Storage implements the redis storage backend for Colly
type Storage struct {
	// Address is the redis server address
	Address string
	// Password is the password for the redis server
	Password string
	// DB is the redis database. Default is 0
	DB int
	// Prefix is an optional string in the keys. It can be used
	// to use one redis database for independent scraping tasks.
	Prefix string
	// Client is the redis connection
	Client *redis.Client

	// Expiration time for Visited keys. After expiration pages
	// are to be visited again.
	Expires time.Duration

	// Max amount of visits to a domain in the given time window
	// defined in Expires.
	DomainVisitLimit int

	mu sync.RWMutex // Only used for cookie methods.
}

// Init initializes the redis storage
func (s *Storage) Init() error {
	if s.Client == nil {
		s.Client = redis.NewClient(&redis.Options{
			Addr:     s.Address,
			Password: s.Password,
			DB:       s.DB,
		})
	}
	_, err := s.Client.Ping().Result()
	if err != nil {
		return fmt.Errorf("Redis connection error: %s", err.Error())
	}
	return err
}

// Clear removes all entries from the storage
func (s *Storage) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.Client.Keys(s.getCookieID("*"))
	keys, err := r.Result()
	if err != nil {
		return err
	}
	r2 := s.Client.Keys(s.Prefix + ":r:*")
	keys2, err := r2.Result()
	if err != nil {
		return err
	}
	keys = append(keys, keys2...)
	keys = append(keys, s.getQueueID())
	return s.Client.Del(keys...).Err()
}

// Visited implements colly/storage.Visited()
func (s *Storage) Visited(requestID uint64) error {
	err := s.Client.Incr(s.getIDStr(requestID)).Err()
	err2 := s.Client.Expire(s.getIDStr(requestID), s.Expires).Err()

	if err != nil {
		return err
	} else if err2 != nil {
		return err2
	}

	return nil
}

// IsVisited implements colly/storage.IsVisited()
func (s *Storage) IsVisited(requestID uint64) (bool, error) {
	cnt, err := s.Client.Get(s.getIDStr(requestID)).Result()
	if err == redis.Nil {
		return false, nil
	} else if err != nil {
		return false, err
	}

	visitCount, err := strconv.Atoi(cnt)
	if err != nil {
		log.Printf("String conversion failed")
		return false, err
	}

	if visitCount <= s.DomainVisitLimit {
		return false, nil
	}

	return true, errors.New("Reached domain visit limit")
}

// SetCookies implements colly/storage..SetCookies()
func (s *Storage) SetCookies(u *url.URL, cookies string) {
	// TODO(js) Cookie methods currently have no way to return an error.

	// We need to use a write lock to prevent a race in the db:
	// if two callers set cookies in a very small window of time,
	// it is possible to drop the new cookies from one caller
	// ('last update wins' == best avoided).
	s.mu.Lock()
	defer s.mu.Unlock()
	// return s.Client.Set(s.getCookieID(u.Host), stringify(cnew), 0).Err()
	err := s.Client.Set(s.getCookieID(u.Host), cookies, 0).Err()
	if err != nil {
		// return nil
		log.Printf("SetCookies() .Set error %s", err)
		return
	}
}

// Cookies implements colly/storage.Cookies()
func (s *Storage) Cookies(u *url.URL) string {
	// TODO(js) Cookie methods currently have no way to return an error.

	s.mu.RLock()
	cookiesStr, err := s.Client.Get(s.getCookieID(u.Host)).Result()
	s.mu.RUnlock()
	if err == redis.Nil {
		cookiesStr = ""
	} else if err != nil {
		// return nil, err
		log.Printf("Cookies() .Get error %s", err)
		return ""
	}
	return cookiesStr
}

// AddRequest implements queue.Storage.AddRequest() function
func (s *Storage) AddRequest(r []byte) error {
	return s.Client.SAdd(s.getQueueID(), r).Err()
}

// GetRequest implements queue.Storage.GetRequest() function
func (s *Storage) GetRequest() ([]byte, error) {
	r, err := s.Client.SPop(s.getQueueID()).Bytes()
	if err != nil {
		return nil, err
	}
	return r, err
}

// QueueSize implements queue.Storage.QueueSize() function
func (s *Storage) QueueSize() (int, error) {
	i, err := s.Client.SCard(s.getQueueID()).Result()
	return int(i), err
}

func (s *Storage) getIDStr(ID uint64) string {
	return fmt.Sprintf("%s:r:%d", s.Prefix, ID)
}

func (s *Storage) getCookieID(c string) string {
	return fmt.Sprintf("%s:c:%s", s.Prefix, c)
}

func (s *Storage) getQueueID() string {
	return fmt.Sprintf("%s:q", s.Prefix)
}
