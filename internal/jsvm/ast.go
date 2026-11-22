package jsvm

type node interface{ at() int }

type nodePos struct{ pos int }

func (n nodePos) at() int { return n.pos }

type (
	identNode struct {
		nodePos
		name string
		ref  *ref
	}
	numNode struct {
		nodePos
		v float64
	}
	strNode struct {
		nodePos
		v string
	}
	tmplNode struct {
		nodePos
		quasis []string
		raws   []string
		exprs  []node
		tag    node
	}
	regexNode struct {
		nodePos
		pattern, flags string
	}
	litNode struct {
		nodePos
		v Value
	}
	thisNode  struct{ nodePos }
	arrayNode struct {
		nodePos
		elems []node
	}
	propNode struct {
		nodePos
		key       node
		computed  bool
		value     node
		shorthand bool
		spread    bool
		method    bool
	}
	objectNode struct {
		nodePos
		props []*propNode
	}
	spreadNode struct {
		nodePos
		arg node
	}
	unaryNode struct {
		nodePos
		op  string
		arg node
	}
	updateNode struct {
		nodePos
		op     string
		prefix bool
		arg    node
	}
	binaryNode struct {
		nodePos
		op          string
		left, right node
	}
	logicalNode struct {
		nodePos
		op          string
		left, right node
	}
	assignNode struct {
		nodePos
		op     string
		target node
		value  node
	}
	condNode struct {
		nodePos
		test, cons, alt node
	}
	callNode struct {
		nodePos
		callee   node
		args     []node
		optional bool
	}
	newNode struct {
		nodePos
		callee node
		args   []node
	}
	memberNode struct {
		nodePos
		obj      node
		name     string
		prop     node
		optional bool
	}
	chainNode struct {
		nodePos
		expr node
	}
	seqNode struct {
		nodePos
		list []node
	}
	awaitNode struct {
		nodePos
		arg node
	}
	funcNode struct {
		nodePos
		name      string
		params    []node
		body      []node
		exprBody  node
		arrow     bool
		async     bool
		isDecl    bool
		method    bool
		scope     *scope
		bodyScope *scope
		selfRef   *binding
		end       int
		simple    bool
		hasAwait  bool
	}
	arrayPattern struct {
		nodePos
		elems []node
	}
	objectPattern struct {
		nodePos
		props []*propNode
		rest  node
	}
	defaultNode struct {
		nodePos
		target node
		value  node
	}
	restNode struct {
		nodePos
		arg node
	}
)

type (
	varDecl struct {
		nodePos
		kind  string
		decls []*declarator
	}
	declarator struct {
		nodePos
		target node
		init   node
	}
	exprStmt struct {
		nodePos
		expr node
	}
	blockStmt struct {
		nodePos
		body  []node
		scope *scope
	}
	emptyStmt  struct{ nodePos }
	returnStmt struct {
		nodePos
		arg node
	}
	ifStmt struct {
		nodePos
		test      node
		cons, alt node
	}
	forStmt struct {
		nodePos
		init   node
		test   node
		update node
		body   node
		scope  *scope
	}
	forInStmt struct {
		nodePos
		of    bool
		left  node
		right node
		body  node
		scope *scope
	}
	whileStmt struct {
		nodePos
		test node
		body node
	}
	doWhileStmt struct {
		nodePos
		body node
		test node
	}
	breakStmt    struct{ nodePos }
	continueStmt struct{ nodePos }
	throwStmt    struct {
		nodePos
		arg node
	}
	tryStmt struct {
		nodePos
		block      *blockStmt
		param      node
		handler    *blockStmt
		finalizer  *blockStmt
		catchScope *scope
	}
	switchCase struct {
		test node
		body []node
	}
	switchStmt struct {
		nodePos
		disc  node
		cases []*switchCase
		scope *scope
	}
)
