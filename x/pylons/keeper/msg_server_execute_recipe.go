package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	"github.com/Pylons-tech/pylons/x/pylons/config"
	"github.com/Pylons-tech/pylons/x/pylons/types"
)

// Match checks if all the constraint match the given item
func Match(item types.Item, itemInput types.ItemInput, ec types.CelEnvCollection) error {
	if itemInput.Doubles != nil {
		for _, param := range itemInput.Doubles {
			double, ok := item.FindDouble(param.Key)
			if !ok {
				return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s key is not available on the item: item_id=%s", param.Key, item.ID))
			}

			if !param.Has(double) {
				return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s key range does not match: item_id=%s", param.Key, item.ID))
			}
		}
	}

	if itemInput.Longs != nil {
		for _, param := range itemInput.Longs {
			long, ok := item.FindLong(param.Key)
			if !ok {
				return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s key is not available on the item: item_id=%s", param.Key, item.ID))
			}

			if !param.Has(long) {
				return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s key range does not match: item_id=%s", param.Key, item.ID))
			}
		}
	}

	if itemInput.Strings != nil {
		for _, param := range itemInput.Strings {
			str, ok := item.FindString(param.Key)
			if !ok {
				return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s key is not available on the item: item_id=%s", param.Key, item.ID))
			}
			if str != param.Value {
				return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s key value does not match: item_id=%s", param.Key, item.ID))
			}
		}
	}

	for _, param := range itemInput.Conditions.Doubles {
		double, err := ec.EvalFloat64(param.Key)
		if err != nil {
			return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s expression is invalid: item_id=%s, %+v", param.Key, item.ID, err))
		}

		dec, err := sdk.NewDecFromStr(fmt.Sprintf("%v", double))
		if err != nil {
			return err
		}

		if !param.Has(dec) {
			return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s expression range does not match: item_id=%s", param.Key, item.ID))
		}
	}

	for _, param := range itemInput.Conditions.Longs {
		long, err := ec.EvalInt64(param.Key)
		if err != nil {
			return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s expression is invalid: item_id=%s, %+v", param.Key, item.ID, err))
		}

		if !param.Has(int(long)) {
			return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s expression range does not match: item_id=%s", param.Key, item.ID))
		}
	}

	for _, param := range itemInput.Conditions.Strings {
		str, err := ec.EvalString(param.Key)
		if err != nil {
			return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s expression is invalid: item_id=%s, %+v", param.Key, item.ID, err))
		}
		if str != param.Value {
			return sdkerrors.Wrap(types.ErrItemMatch, fmt.Sprintf("%s expression value does not match: item_id=%s", param.Key, item.ID))
		}
	}
	return nil
}

func (k msgServer) MatchItemInputs(ctx sdk.Context, inputItemsIDs []string, recipe types.Recipe) ([]types.Item, error) {
	if len(inputItemsIDs) != len(recipe.ItemInputs) {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "size mismatch between provided input items and items required by recipe")
	}
	matchedItems := make([]types.Item, len(recipe.ItemInputs))

	// build Item list from inputItemIDs
	inputItemMap := make(map[string]types.Item)
	checkedInputItems := make([]bool, len(inputItemsIDs))

	for i, recipeItemInput := range recipe.ItemInputs {
		var err error
		for j, id := range inputItemsIDs {
			if checkedInputItems[j] {
				continue
			}
			inputItem, found := inputItemMap[id]
			if !found {
				inputItem, found = k.GetItem(ctx, recipe.CookbookID, recipe.ID, id)
				if !found {
					return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, fmt.Sprintf("item with id %v not found", id))
				}
			}
			inputItemMap[id] = inputItem
			// match
			var ec types.CelEnvCollection
			ec, err = k.EnvCollection(ctx, recipe.ID, "", inputItem)
			if err != nil {
				return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
			}
			err = Match(inputItem, recipeItemInput, ec)
			if err != nil {
				matchedItems[i] = inputItem
				checkedInputItems[j] = true
				break
			}
		}
		if err != nil {
			return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "cannot find match for recipe input item ")
		}
	}
	return matchedItems, nil
}

func (k msgServer) ExecuteRecipe(goCtx context.Context, msg *types.MsgExecuteRecipe) (*types.MsgExecuteRecipeResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	recipe, found := k.GetRecipe(ctx, msg.CookbookID, msg.RecipeID)
	if !found {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, "could not find recipe")
	}
	matchedItems, err := k.MatchItemInputs(ctx, msg.ItemIDs, recipe)
	if err != nil {
		return nil, sdkerrors.Wrap(sdkerrors.ErrInvalidRequest, err.Error())
	}

	// TODO check that user has balance to cover recipe.CoinInputs
	// if true, lock these coins

	// create ItemRecord list
	itemRecords := make([]types.ItemRecord, len(matchedItems))
	for i, item := range matchedItems {
		itemRecords[i] = types.ItemRecord{
			ID:      item.ID,
			Doubles: item.Doubles,
			Longs:   item.Longs,
			Strings: item.Strings,
		}
	}
	// create PendingExecution passing the current blockHeight
	execution := types.Execution{
		Creator:     msg.Creator,
		CookbookID:  msg.CookbookID,
		RecipeID:    msg.RecipeID,
		NodeVersion: config.GetNodeVersionString(),
		BlockHeight: uint64(ctx.BlockHeight()),
		CoinInputs:  nil, // TODO
		ItemInputs:  itemRecords,
	}

	id := k.AppendPendingExecution(ctx, execution)
	return &types.MsgExecuteRecipeResponse{ID: id}, nil
}
