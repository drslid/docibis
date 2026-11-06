package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"docibis/internal/merge"
)

// includeBlocks puts in a template its shared blocks (<<ref:name>>, or
// <<refLookup:field>> named by the data): the published version of each,
// from the library.
func (s *Server) includeBlocks(ctx context.Context, odt []byte, data map[string]any) ([]byte, error) {
	out, err := merge.Include(odt, data, func(name string) ([]byte, error) {
		t, v, err := s.findTemplate(ctx, name)
		var te *tryError
		switch {
		case errors.As(err, &te):
			return nil, refuse("Bloc partagé <<ref:%s>> : %s", name, te.msg)
		case err != nil:
			return nil, err
		case t.Kind != "template":
			return nil, refuse("Bloc partagé <<ref:%s>> : un PDF ne s'insère pas dans un document.", name)
		case len(v.Prepared) == 0:
			return nil, &tryError{http.StatusServiceUnavailable, fmt.Sprintf("Bloc partagé <<ref:%s>> : pas encore prêt, réessayez dans un instant.", name)}
		}
		return v.Prepared, nil
	})
	var be *merge.ErrBlock
	if errors.As(err, &be) {
		return nil, refuse("Blocs partagés : %s.", be.Msg)
	}
	return out, err
}
