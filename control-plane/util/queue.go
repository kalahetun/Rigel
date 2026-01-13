package util

import (
	"container/list"
	"fmt"
	"sync"
)

// ----------------------- FixedQueue -----------------------
// 线程安全固定大小队列，支持自动弹出最老元素
type FixedQueue struct {
	list *list.List
	size int
	mu   sync.Mutex
}

// NewFixedQueue 创建固定大小队列
func NewFixedQueue(size int) *FixedQueue {
	if size <= 0 {
		panic("size must be greater than 0")
	}
	return &FixedQueue{
		list: list.New(),
		size: size,
	}
}

// Push 入队，如果满了会自动弹出最老的元素
func (q *FixedQueue) Push(value interface{}) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.list.Len() >= q.size {
		q.list.Remove(q.list.Front())
	}
	q.list.PushBack(value)
}

// Pop 弹出最老的元素
func (q *FixedQueue) Pop() interface{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.list.Len() == 0 {
		return nil
	}
	e := q.list.Front()
	q.list.Remove(e)
	return e.Value
}

func (q *FixedQueue) Latest() interface{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.list.Len() == 0 {
		return nil
	}
	return q.list.Back().Value
}

// Len 返回队列当前长度
func (q *FixedQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.list.Len()
}

// Print 打印队列内容
func (q *FixedQueue) Print() {
	q.mu.Lock()
	defer q.mu.Unlock()

	fmt.Print("Queue: ")
	for e := q.list.Front(); e != nil; e = e.Next() {
		fmt.Printf("%v ", e.Value)
	}
	fmt.Println()
}
