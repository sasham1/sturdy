package routes

import (
	"getsturdy.com/api/pkg/author"
	"getsturdy.com/api/pkg/codebase/db"
	db_user "getsturdy.com/api/pkg/user/db"
)

func membersAsAuthors(codebaseUserRepo db.CodebaseUserRepository, userRepo db_user.Repository, codebaseID string) ([]author.Author, error) {
	// Get members
	members, err := codebaseUserRepo.GetByCodebase(codebaseID)
	if err != nil {
		return nil, err
	}

	var memberAuthors []author.Author
	for _, m := range members {
		userAuthor, err := author.GetAuthor(m.UserID, userRepo)
		if err != nil {
			return nil, err
		}
		memberAuthors = append(memberAuthors, userAuthor)
	}

	return memberAuthors, nil
}
