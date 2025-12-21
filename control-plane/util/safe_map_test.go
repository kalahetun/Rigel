package util

import (
	"errors"
	"sync"
	"testing"
)

// TestSafeMap_BasicOps 测试基本操作（Set/Get/Delete/Len）
func TestSafeMap_BasicOps(t *testing.T) {
	sm := NewSafeMap()

	// 测试初始长度为0
	if sm.Len() != 0 {
		t.Errorf("初始长度错误，期望0，实际%d", sm.Len())
	}

	// 测试Set和Get
	key := "test_key"
	value := "test_value"
	sm.Set(key, value)

	if sm.Len() != 1 {
		t.Errorf("Set后长度错误，期望1，实际%d", sm.Len())
	}

	val, ok := sm.Get(key)
	if !ok {
		t.Error("Get未找到已Set的key")
	}
	if val != value {
		t.Errorf("Get值错误，期望%v，实际%v", value, val)
	}

	// 测试Get不存在的key
	nonExistVal, nonExistOk := sm.Get("non_exist")
	if nonExistOk {
		t.Error("Get不存在的key返回了true")
	}
	if nonExistVal != nil {
		t.Errorf("Get不存在的key返回了非nil值：%v", nonExistVal)
	}

	// 测试Delete
	sm.Delete(key)
	if sm.Len() != 0 {
		t.Errorf("Delete后长度错误，期望0，实际%d", sm.Len())
	}
	delVal, delOk := sm.Get(key)
	if delOk {
		t.Error("Delete后仍能获取到key")
	}
	if delVal != nil {
		t.Errorf("Delete后返回了非nil值：%v", delVal)
	}

	// 测试Delete不存在的key（无panic）
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Error("Delete不存在的key触发panic：", r)
			}
		}()
		sm.Delete("non_exist")
	}()
}

// TestSafeMap_Range 测试Range遍历
func TestSafeMap_Range(t *testing.T) {
	sm := NewSafeMap()
	testData := map[string]interface{}{
		"k1": 123,
		"k2": "hello",
		"k3": []int{1, 2, 3},
	}

	// 写入测试数据
	for k, v := range testData {
		sm.Set(k, v)
	}

	// 遍历并验证
	visited := make(map[string]bool)
	sm.Range(func(key string, value interface{}) bool {
		expectedVal, ok := testData[key]
		if !ok {
			t.Errorf("Range遍历到未知key：%s", key)
			return false
		}
		if value != expectedVal {
			t.Errorf("Range遍历key=%s值错误，期望%v，实际%v", key, expectedVal, value)
			return false
		}
		visited[key] = true
		return true
	})

	// 验证所有key都被遍历到
	for k := range testData {
		if !visited[k] {
			t.Errorf("Range未遍历到key：%s", k)
		}
	}

	// 测试遍历中途停止
	stopKey := "k2"
	stopCount := 0
	sm.Range(func(key string, value interface{}) bool {
		stopCount++
		return key != stopKey // 遇到k2停止
	})
	if stopCount != 2 { // k1 → k2（停止），共2次
		t.Errorf("Range中途停止错误，期望遍历2次，实际%d次", stopCount)
	}
}

// TestSafeMap_GetAll 测试GetAll快照
func TestSafeMap_GetAll(t *testing.T) {
	sm := NewSafeMap()
	testData := map[string]interface{}{
		"a": 1,
		"b": 2,
	}

	for k, v := range testData {
		sm.Set(k, v)
	}

	// 获取快照
	snapshot := sm.GetAll()

	// 验证快照长度和内容
	if len(snapshot) != len(testData) {
		t.Errorf("GetAll快照长度错误，期望%d，实际%d", len(testData), len(snapshot))
	}
	for k, v := range testData {
		if snapshot[k] != v {
			t.Errorf("GetAll快照key=%s值错误，期望%v，实际%v", k, v, snapshot[k])
		}
	}

	// 修改原map，验证快照不受影响
	sm.Set("a", 999)
	if snapshot["a"] != 1 {
		t.Error("GetAll快照被原map修改影响")
	}
}

// TestSafeMap_Update 测试Update方法
func TestSafeMap_Update(t *testing.T) {
	sm := NewSafeMap()

	// 测试更新不存在的key
	err := sm.Update("non_exist", func(value interface{}) error {
		return nil
	})
	if err == nil || err.Error() != "key not found" {
		t.Errorf("Update不存在的key错误，期望key not found，实际%v", err)
	}

	// 测试更新存在的key（修改结构体示例）
	type testStruct struct {
		Num int
	}
	key := "struct_key"
	sm.Set(key, &testStruct{Num: 10})

	// 执行更新
	err = sm.Update(key, func(value interface{}) error {
		ts, ok := value.(*testStruct)
		if !ok {
			return errors.New("类型断言失败")
		}
		ts.Num = 20
		return nil
	})
	if err != nil {
		t.Fatalf("Update失败：%v", err)
	}

	// 验证更新结果
	val, _ := sm.Get(key)
	ts := val.(*testStruct)
	if ts.Num != 20 {
		t.Errorf("Update后值错误，期望20，实际%d", ts.Num)
	}

	// 测试Update中返回错误
	err = sm.Update(key, func(value interface{}) error {
		return errors.New("自定义错误")
	})
	if err == nil || err.Error() != "自定义错误" {
		t.Errorf("Update返回错误不符合预期，期望自定义错误，实际%v", err)
	}
}

// TestSafeMap_Concurrency 测试并发安全
func TestSafeMap_Concurrency(t *testing.T) {
	sm := NewSafeMap()
	var wg sync.WaitGroup
	loopCount := 1000

	// 并发Set
	wg.Add(loopCount)
	for i := 0; i < loopCount; i++ {
		go func(idx int) {
			defer wg.Done()
			key := "concurrent_key_" + string(idx)
			sm.Set(key, idx)
		}(i)
	}
	wg.Wait()

	// 验证Set结果
	if sm.Len() != loopCount {
		t.Errorf("并发Set后长度错误，期望%d，实际%d", loopCount, sm.Len())
	}

	// 并发Get和Delete
	wg.Add(loopCount * 2)
	for i := 0; i < loopCount; i++ {
		go func(idx int) {
			defer wg.Done()
			key := "concurrent_key_" + string(idx)
			_, ok := sm.Get(key)
			if !ok {
				t.Errorf("并发Get未找到key：%s", key)
			}
		}(i)

		go func(idx int) {
			defer wg.Done()
			key := "concurrent_key_" + string(idx)
			sm.Delete(key)
		}(i)
	}
	wg.Wait()

	// 验证最终长度为0
	if sm.Len() != 0 {
		t.Errorf("并发Delete后长度错误，期望0，实际%d", sm.Len())
	}
}
