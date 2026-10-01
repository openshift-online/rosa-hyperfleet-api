package reader

import "fmt"

// ShardSpec restricts a List or Watch to rows whose shard key hashes,
// modulo Mod, into Owned. Nil *ShardSpec means unsharded.
//
// The shard key is the namespace unless KeyLabel is set. With KeyLabel, it is
// the value of that label, or the row's own uid when the label is absent: an
// owner and the objects carrying its uid in KeyLabel land on the same shard.
// Shard membership must be immutable per object: the namespace is part of the
// primary key, and a KeyLabel must be set at create and never changed (a
// watcher does not see an object move out of its shard).
type ShardSpec struct {
	Mod      int    // hash modulus, > 0
	Owned    []int  // residues owned by this replica, each in [0, Mod)
	KeyLabel string // optional label whose value is the shard key
}

func (s *ShardSpec) Validate() error {
	if s.Mod <= 0 {
		return fmt.Errorf("shard: Mod must be > 0, got %d", s.Mod)
	}
	if len(s.Owned) == 0 {
		return fmt.Errorf("shard: Owned must be non-empty")
	}
	seen := make(map[int]bool, len(s.Owned))
	for _, o := range s.Owned {
		if o < 0 || o >= s.Mod {
			return fmt.Errorf("shard: Owned value %d out of range [0, %d)", o, s.Mod)
		}
		if seen[o] {
			return fmt.Errorf("shard: duplicate Owned value %d", o)
		}
		seen[o] = true
	}
	return nil
}

func (s *ShardSpec) shardClause(startParam int) (string, []any) {
	if s.KeyLabel == "" {
		clause := fmt.Sprintf(
			"abs(hashtext(namespace)::bigint) %% $%d = ANY($%d::int[])",
			startParam, startParam+1)
		return clause, []any{s.Mod, s.Owned}
	}
	clause := fmt.Sprintf(
		"abs(hashtext(COALESCE(metadata->'labels'->>$%d, uid::text))::bigint) %% $%d = ANY($%d::int[])",
		startParam, startParam+1, startParam+2)
	return clause, []any{s.KeyLabel, s.Mod, s.Owned}
}

// AppendQuery appends the shard WHERE predicate to query and args.
// Parameter numbering is derived from len(args).
func (s *ShardSpec) AppendQuery(query string, args []any) (string, []any) {
	clause, shardArgs := s.shardClause(len(args) + 1)
	return query + " AND " + clause, append(args, shardArgs...)
}

// ToListFilter returns a ListFilter with the shard predicate for use with List().
func (s *ShardSpec) ToListFilter() *ListFilter {
	clause, args := s.shardClause(2)
	return &ListFilter{
		WhereClauses: []string{clause},
		WhereArgs:    args,
	}
}
