package local_info_manager

//1、定时器 读storage文件 汇聚group信息 到etcd 并且 加入一个全局的 queue供 elastic scaling使用
