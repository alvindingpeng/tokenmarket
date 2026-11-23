package op

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 本文件是并发抖动那一批修法的回归护栏。
//
// 它要守住的不变量只有一条: **op 里每一处显式事务, 事务内执行的第一条 SQL 要么本身就是写,
// 要么开头就取写锁。** 也就是「没有一处落在短小的读后写」。
//
// 为什么这值得一条静态用例: 读后写的事务在 WAL 下会撞 SQLITE_BUSY_SNAPSHOT(517),
// 而 busy_timeout 对它无效; 表现只是「偶发一条事务失败」, 单机低并发下几乎看不见。
// 以后谁在这 40 处里加一句先行 SELECT、或把开头的 AcquireWriteLock 挪到读之后, 就重新打开
// 那个窗口, 而 review 时很难看出来 —— 这类缺陷只能靠静态断言守住。
//
// 为什么用静态 AST 扫描而不是运行时 GORM 回调(两个方案都试过):
//   1. 运行时方案只覆盖「被用例真正驱动过的那几个 op」, 静态覆盖全部 40 处;
//   2. create/update/delete 默认各包一层隐式单语句事务, 运行时按 ConnPool 是不是 *sql.Tx
//      判「在不在显式事务里」会把单条 db.Create 也算进来, 结论就乱了;
//   3. Before 锚点里 Statement.SQL 还是空串(实测), 断言会静默空转。
// 静态扫描的失败信息还能指到「文件:行 函数名」, 与 docs/billing-retention-180d.md §9.3 的
// 清单逐行对应。

// txnKind 是一条 terminal 语句的类别。
type txnKind string

const (
	kindWrite txnKind = "write"
	kindRead  txnKind = "read"
	kindLock  txnKind = "lock"
)

// terminalMethods 只收**真正发 SQL** 的方法名。
// Model/Select/Where/Order/Limit/Preload 这类一律不在表里: 它们只往 Statement 上挂子句,
// 不发语句 —— 把它们当成「第一条语句」正是最容易犯的误判。
var terminalMethods = map[string]txnKind{
	"Create":          kindWrite,
	"CreateInBatches": kindWrite,
	"Save":            kindWrite,
	"Update":          kindWrite,
	"Updates":         kindWrite,
	"UpdateColumn":    kindWrite,
	"UpdateColumns":   kindWrite,
	"Delete":          kindWrite,
	"Exec":            kindWrite,
	"FirstOrCreate":   kindWrite,
	"First":           kindRead,
	"Last":            kindRead,
	"Take":            kindRead,
	"Find":            kindRead,
	"FindInBatches":   kindRead,
	"Scan":            kindRead,
	"Count":           kindRead,
	"Pluck":           kindRead,
	"Rows":            kindRead,
	"Row":             kindRead,
}

// lockFunc 是取写锁的入口; 它自己就是一条写(零命中的锚点 UPDATE)。
const lockFunc = "AcquireWriteLock"

// 递归展开本包 helper 的深度上限。三到四层足够覆盖 walletCreditTx 与 clearActiveItems 这类
// 单行委托; 再深就说明这处事务的形状已经不适合静态判定, 应当让用例红而不是悄悄放行。
const maxExpandDepth = 4

type txnTerminal struct {
	kind   txnKind
	method string
	line   int
}

type txnSite struct {
	file  string
	line  int
	fn    string
	terms []txnTerminal
}

func (s txnSite) label() string {
	return s.file + ":" + strconv.Itoa(s.line) + " " + s.fn
}

type txnScanner struct {
	fset  *token.FileSet
	funcs map[string]*ast.FuncDecl
}

// TestTxLockFirstStatementIsWrite 是本护栏的主断言。
func TestTxLockFirstStatementIsWrite(t *testing.T) {
	sites := scanTransactionSites(t)

	counts := map[txnKind]int{}
	for _, site := range sites {
		if len(site.terms) == 0 {
			// 事务体里一条 terminal 都没有: 只可能是「委托给 helper 却没展开」, 那会把真问题
			// 变成假绿。真空事务不该存在, 若真有也必须显式白名单。
			t.Fatalf("%s: 事务体内没找到任何执行语句 —— 首条语句可能在被委托的 helper 里, 请检查递归展开; 若是真空事务须显式加白名单并写清理由", site.label())
		}
		first := site.terms[0]
		// 白名单**先于**分类判定: 这两处的性质与主断言相反, 所以它们的额外校验必须在
		// 「首条是读」之外也能命中 —— 给只读事务加锁后, 它的首条会变成取锁(不再走读分支),
		// 若只在读分支里检查白名单, 那条例外就失去意义了(实测: 只靠总数断言能红, 但报的是
		// 「A 类处数多了一处」, 而不是「只读事务里出现了写」)。
		if rule, allowed := readFirstAllowList[allowKey(site)]; allowed {
			if err := rule.check(site); err != nil {
				t.Fatalf("%s: %v", site.label(), err)
			}
			counts[rule.kind]++
			continue
		}
		if first.kind == kindRead {
			t.Fatalf("%s: 事务内首条语句是读(%s, 见第 %d 行), 期望写: 读后写在 WAL 下会撞 SQLITE_BUSY_SNAPSHOT(517), busy_timeout 管不了它。只有两种修法 —— 事务开头加 db.AcquireWriteLock(tx), 或把这条读挪到事务外", site.label(), first.method, first.line)
		}
		counts[first.kind]++
	}

	// 处数与四类计数必须同时与 §9.1 的表一致。
	//
	// 顺序很重要: 逐处判定**先**跑。新增一处「读后写」的事务时, 上面那条会先报出可操作的
	// 结论(「事务内首条语句是读」), 而不是只报一句「处数 = 40」—— 后者把"该加锁还是该白名单"
	// 这个真正要做的判断留给下一个人去猜。处数与计数在这里兜的是另一类问题: 扫描器自己坏了
	// (递归展开或 terminal 名单漏项)会安静地少报, 那比误报危险得多。
	if len(sites) != wantSiteCount {
		t.Fatalf("显式事务处数 = %d, 期望 %d: 事务被新增或删除时, 必须重新判定它属于哪一类, 并把新清单同步到 docs/billing-retention-180d.md §9.3 与本用例的计数", len(sites), wantSiteCount)
	}
	for _, want := range []struct {
		kind txnKind
		want int
		note string
	}{
		{kindLock, wantLockFirst, "§9.3 A 类(开头已取写锁)"},
		{kindWrite, wantWriteFirst, "§9.3 B 类(首条即写)"},
		{kindReadOnly, wantReadOnly, "§9.3 C 类(纯只读事务)"},
		{kindLongTx, wantLongTx, "§9.3 D 类(长事务)"},
	} {
		if counts[want.kind] != want.want {
			t.Fatalf("%s 的处数 = %d, 期望 %d: 少见一处通常意味着那处 AcquireWriteLock 被删或挪到读之后, 多一处则说明事务形状变了", want.note, counts[want.kind], want.want)
		}
	}
}

const (
	wantSiteCount  = 40
	wantLockFirst  = 18
	wantWriteFirst = 20
	wantReadOnly   = 1
	wantLongTx     = 1
)

// 两类例外各自的方向与主断言相反, 所以它们是独立的分类而不是「跳过」。
const (
	kindReadOnly txnKind = "read-only"
	kindLongTx   txnKind = "long-tx"
)

// allowRule 是一条例外规则: **逐条显式列出**, 不用「函数名含 backup 就跳过」这类正则
// —— 正则白名单会随重命名静默失效, 而那正好是它该报警的时候。
type allowRule struct {
	kind  txnKind
	check func(site txnSite) error
}

// allowKey 用「文件名 + 函数名」作键。扫描器给的是包内相对文件名(glob "*.go" 的结果),
// 所以这里用 Base 归一, 免得白名单写成 internal/op/xxx.go 之后永远匹配不上 —— 那种失配的
// 表现是"白名单形同虚设", 与没有白名单一样危险。
func allowKey(site txnSite) string {
	return filepath.Base(site.file) + "|" + site.fn
}

var readFirstAllowList = map[string]allowRule{
	"settlement.go|settlementAggregateDay": {
		kind: kindReadOnly,
		check: func(site txnSite) error {
			for _, term := range site.terms {
				if term.kind == kindWrite || term.kind == kindLock {
					return fmt.Errorf("只读事务里出现了写语句(%s, 第 %d 行): 结算聚合必须是自洽快照, 一旦它开始写就不再是只读事务, 这条白名单的结论也跟着失效", term.method, term.line)
				}
			}
			return nil
		},
	},
	"backup.go|DBImportIncremental": {
		kind: kindLongTx,
		check: func(site txnSite) error {
			for _, term := range site.terms {
				if term.kind == kindLock {
					return fmt.Errorf("长事务里出现了取锁(%s, 第 %d 行): 整份导入期间取写锁会堵死全库写入口, 比它想避免的偶发 517 贵得多; 长事务的正解是拆短或外层重试, 不是上锁", term.method, term.line)
				}
			}
			return nil
		},
	},
}

// scanTransactionSites 解析当前目录下全部**非测试**源码, 找出每一处显式事务。
func scanTransactionSites(t *testing.T) []txnSite {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob go files: %v", err)
	}
	scanner := &txnScanner{fset: token.NewFileSet(), funcs: map[string]*ast.FuncDecl{}}
	var sources []struct {
		name string
		file *ast.File
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, perr := parser.ParseFile(scanner.fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		sources = append(sources, struct {
			name string
			file *ast.File
		}{path, parsed})
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name != nil {
				if _, exists := scanner.funcs[fn.Name.Name]; !exists {
					scanner.funcs[fn.Name.Name] = fn
				}
			}
		}
	}
	var sites []txnSite
	for _, source := range sources {
		for _, decl := range source.file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			hostName := "<匿名>"
			if fn.Name != nil {
				hostName = fn.Name.Name
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				lit := transactionFuncLit(call)
				if lit == nil {
					return true
				}
				position := scanner.fset.Position(call.Lparen)
				if position.Filename != source.name {
					return true
				}
				site := txnSite{file: source.name, line: position.Line, fn: hostName}
				scanner.collectStmts(lit.Body.List, 0, &site.terms)
				sites = append(sites, site)
				return true
			})
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].file != sites[j].file {
			return sites[i].file < sites[j].file
		}
		return sites[i].line < sites[j].line
	})
	return sites
}

// transactionFuncLit 从 Transaction(...) 的实参里取出 func(tx *gorm.DB) error 这个函数字面量。
// 事务体的语句在这个 funclit 的 body 里, 与 Transaction( 出现在哪个外层函数无关 —— 这一点
// 很容易看错。
func transactionFuncLit(call *ast.CallExpr) *ast.FuncLit {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		if fn.Sel.Name != "Transaction" {
			return nil
		}
	case *ast.Ident:
		if fn.Name != "channelModelTransaction" {
			return nil
		}
	default:
		return nil
	}
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.FuncLit)
		if !ok || lit.Body == nil || lit.Type == nil || lit.Type.Params == nil {
			continue
		}
		params := lit.Type.Params.List
		if len(params) != 1 || len(params[0].Names) != 1 {
			continue
		}
		star, ok := params[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		selector, ok := star.X.(*ast.SelectorExpr)
		if !ok || selector.Sel == nil || selector.Sel.Name != "DB" {
			continue
		}
		return lit
	}
	return nil
}

// collectStmts 按**执行顺序**收集事务体内的 terminal 语句。
func (s *txnScanner) collectStmts(list []ast.Stmt, depth int, out *[]txnTerminal) {
	if depth > maxExpandDepth {
		return
	}
	for _, stmt := range list {
		s.collectStmt(stmt, depth, out)
	}
}

func (s *txnScanner) collectStmt(stmt ast.Stmt, depth int, out *[]txnTerminal) {
	switch typed := stmt.(type) {
	case *ast.BlockStmt:
		s.collectStmts(typed.List, depth, out)
	case *ast.ExprStmt:
		s.collectExpr(typed.X, depth, out)
	case *ast.AssignStmt:
		for _, rhs := range typed.Rhs {
			s.collectExpr(rhs, depth, out)
		}
	case *ast.ReturnStmt:
		for _, result := range typed.Results {
			s.collectExpr(result, depth, out)
		}
	case *ast.IfStmt:
		if typed.Init != nil {
			s.collectStmt(typed.Init, depth, out)
		}
		s.collectExpr(typed.Cond, depth, out)
		s.collectStmts(typed.Body.List, depth, out)
		if typed.Else != nil {
			s.collectStmt(typed.Else, depth, out)
		}
	case *ast.ForStmt:
		if typed.Init != nil {
			s.collectStmt(typed.Init, depth, out)
		}
		s.collectStmts(typed.Body.List, depth, out)
	case *ast.RangeStmt:
		s.collectExpr(typed.X, depth, out)
		s.collectStmts(typed.Body.List, depth, out)
	case *ast.LabeledStmt:
		s.collectStmt(typed.Stmt, depth, out)
	case *ast.SwitchStmt:
		if typed.Tag != nil {
			s.collectExpr(typed.Tag, depth, out)
		}
		s.collectStmts(typed.Body.List, depth, out)
	case *ast.TypeSwitchStmt:
		s.collectStmts(typed.Body.List, depth, out)
	case *ast.DeclStmt:
		if gen, ok := typed.Decl.(*ast.GenDecl); ok {
			for _, spec := range gen.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for _, expr := range value.Values {
						s.collectExpr(expr, depth, out)
					}
				}
			}
		}
	}
}

func (s *txnScanner) collectExpr(expr ast.Expr, depth int, out *[]txnTerminal) {
	if expr == nil || depth > maxExpandDepth {
		return
	}
	switch typed := expr.(type) {
	case *ast.ParenExpr:
		s.collectExpr(typed.X, depth, out)
	case *ast.CallExpr:
		for _, arg := range typed.Args {
			s.collectExpr(arg, depth, out)
		}
		if selector, ok := typed.Fun.(*ast.SelectorExpr); ok && selector.Sel != nil {
			name := selector.Sel.Name
			if name == lockFunc {
				*out = append(*out, s.terminalAt(kindLock, name, typed.Lparen))
				return
			}
			if kind, isTerminal := terminalMethods[name]; isTerminal {
				// terminal: 整条链到这里执行完了, 接收者链只是 build, 不再往下走。
				*out = append(*out, s.terminalAt(kind, name, typed.Lparen))
				return
			}
			s.collectExpr(selector.X, depth, out)
			return
		}
		if ident, ok := typed.Fun.(*ast.Ident); ok {
			// 包内 helper 递归展开: 事务体只有一行委托时(return walletCreditTx(tx, ...)),
			// 真正的首条语句在下一层。不展开会把这类事务读成「空事务」而漏检。
			if decl, found := s.funcs[ident.Name]; found && decl.Body != nil {
				// 只在 helper 的参数里出现过 tx 时才展开, 免得把无关工具函数也算进来。
				if helperMentionsTx(decl) {
					s.collectStmts(decl.Body.List, depth+1, out)
				}
			}
			return
		}
		return
	case *ast.SelectorExpr:
		// `.Error` / `.Rows()` 这类选择子: 真正的语句在接收者链上。前面漏了这一支,
		// 导致 `if err := tx.Exec(...).Error; err != nil` 这种最常见的取锁/写首句被读成
		// 「没有语句」—— 用例报的正是 billing_recovery.go:101 那种形态。
		s.collectExpr(typed.X, depth, out)
	case *ast.BinaryExpr:
		s.collectExpr(typed.X, depth, out)
		s.collectExpr(typed.Y, depth, out)
	case *ast.UnaryExpr:
		s.collectExpr(typed.X, depth, out)
	case *ast.TypeAssertExpr:
		s.collectExpr(typed.X, depth, out)
	case *ast.StarExpr:
		s.collectExpr(typed.X, depth, out)
	case *ast.CompositeLit:
		for _, element := range typed.Elts {
			if inner, ok := element.(ast.Expr); ok {
				s.collectExpr(inner, depth, out)
			}
		}
	case *ast.KeyValueExpr:
		s.collectExpr(typed.Value, depth, out)
	}
}

// helperMentionsTx 报告该函数的任一名为 tx 的参数(或接收者)是否被函数体引用过。
func helperMentionsTx(decl *ast.FuncDecl) bool {
	if decl.Type == nil || decl.Type.Params == nil || decl.Body == nil {
		return false
	}
	named := false
	for _, field := range decl.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == "tx" {
				named = true
			}
		}
	}
	if !named {
		return false
	}
	used := false
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == "tx" {
			used = true
		}
		return true
	})
	return used
}

func (s *txnScanner) terminalAt(kind txnKind, method string, offset token.Pos) txnTerminal {
	return txnTerminal{kind: kind, method: method, line: s.fset.Position(offset).Line}
}
