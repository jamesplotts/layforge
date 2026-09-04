// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/jamesplotts/layforge/master/internal/policy"
	"github.com/jamesplotts/layforge/master/internal/store"
	"github.com/jamesplotts/layforge/master/internal/systemenginepb"
)

// pvpGateBlocked reports whether taking actionNoun (e.g. "an item",
// "currency") away from source — on behalf of actingSenderID, not
// source's own owner — should be blocked under campaignID's PvP policy
// (design doc §9.1). This is the shared gate dmGiveItem and
// dmTransferCurrency (dm_tools.go) already used inline before this
// vendor pass extracted it, now also reused by dmVendorSellItem's vendor
// leg and dmVendorBuyItem's seller leg — the same real, engine-computed
// "is source already dead" check (characterIsDead, turn_order.go) in
// every case, never the DM model's own say-so.
//
// A non-empty blockedReason means the action must not proceed; the
// caller should return (blockedReason, false, toolErrorCode) from its
// own tool function. A nil error with an empty blockedReason means the
// action is permitted. A non-nil error means the underlying
// characterIsDead check itself failed (an engine_error, not a PvP
// rejection) — the caller should surface it as such.
func (s *Server) pvpGateBlocked(ctx context.Context, campaignID, actingSenderID, actionNoun string, source store.Character) (blockedReason string, toolErrorCode string, err error) {
	if source.OwnerID == "" || source.OwnerID == masterSenderID || source.OwnerID == actingSenderID {
		return "", "", nil
	}
	sourceDead, err := s.characterIsDead(ctx, source)
	if err != nil {
		return "", "", err
	}
	if sourceDead {
		return "", "", nil
	}
	pol := s.campaignPolicy(ctx, campaignID)
	switch pol.PvPPolicy {
	case policy.PvPPolicyAllowed:
		return "", "", nil
	case policy.PvPPolicyWithConsent:
		if slices.Contains(pol.PvPConsent, source.OwnerID) {
			return "", "", nil
		}
		return fmt.Sprintf("PvP blocked: this campaign's policy is pvp_with_consent, and %s has not consented to PvP effects", source.OwnerID), "pvp_no_consent", nil
	default:
		return fmt.Sprintf("PvP blocked: this campaign's policy does not allow one player's action to take %s from another player's character (%s)", actionNoun, source.OwnerID), "pvp_blocked", nil
	}
}

// copperToDenominations decomposes totalCopper into the fewest coins —
// platinum first, down to copper — the same greedy algorithm
// GetItemInfo's engine-side implementation uses, applied here after
// Master's own price_multiplier adjustment (which the engine never
// computes — see policy.CampaignPolicy.PriceMultiplier's doc comment).
func copperToDenominations(totalCopper int64) (copper, silver, gold, platinum int32) {
	remaining := totalCopper
	platinum = int32(remaining / 1000)
	remaining %= 1000
	gold = int32(remaining / 100)
	remaining %= 100
	silver = int32(remaining / 10)
	remaining %= 10
	copper = int32(remaining)
	return copper, silver, gold, platinum
}

// denominationsToCopper is copperToDenominations' inverse.
func denominationsToCopper(copper, silver, gold, platinum int32) int64 {
	return int64(copper) + int64(silver)*10 + int64(gold)*100 + int64(platinum)*1000
}

// itemPriceCopper looks up itemName's real base price via GetItemInfo and
// applies campaignID's price_multiplier — the one place vendor pricing is
// actually computed, so check_item_price/list_vendor_inventory/
// vendor_sell_item/vendor_buy_item all agree on the same number. Returns
// ok = false (with a human-readable reason) when the engine doesn't
// recognize itemName — a real rejection, never a guessed price.
func (s *Server) itemPriceCopper(ctx context.Context, campaignID, itemName string) (priceCopper int64, ok bool, reason string, err error) {
	resp, err := s.systemEngine.GetItemInfo(ctx, &systemenginepb.GetItemInfoRequest{
		RequestId: "dm-tool-price-" + itemName,
		ItemName:  itemName,
	})
	if err != nil {
		return 0, false, "", fmt.Errorf("calling system engine: %w", err)
	}
	if !resp.Success {
		return 0, false, resp.Error, nil
	}
	base := denominationsToCopper(resp.Copper, resp.Silver, resp.Gold, resp.Platinum)
	multiplier := s.campaignPolicy(ctx, campaignID).EffectivePriceMultiplier()
	adjusted := int64(math.Round(float64(base) * multiplier))
	return adjusted, true, "", nil
}

// dmCheckItemPrice looks up a real item's price — never a guessed or
// model-invented number (design doc §8, §9) — see itemPriceCopper.
func (s *Server) dmCheckItemPrice(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		ItemName string `json:"item_name"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	priceCopper, ok, reason, err := s.itemPriceCopper(ctx, campaignID, args.ItemName)
	if err != nil {
		return err.Error(), false, "engine_error"
	}
	if !ok {
		return reason, false, "item_not_found"
	}

	copper, silver, gold, platinum := copperToDenominations(priceCopper)
	payload, err := json.Marshal(map[string]any{
		"item_name": args.ItemName,
		"copper":    copper,
		"silver":    silver,
		"gold":      gold,
		"platinum":  platinum,
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmListVendorInventory returns the full, real list of items characterID
// actually holds, each with its real price — see itemPriceCopper. Exists
// because character_data's inventory is opaque to Master (Actor's own
// doc comment); ListInventory is the real engine RPC that answers "what
// does this character actually have," so this tool never has to guess at
// character_data's shape itself (CLAUDE.md's system-engine boundary
// rule).
func (s *Server) dmListVendorInventory(ctx context.Context, campaignID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		CharacterID string `json:"character_id"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	character, err := s.campaignCharacter(ctx, campaignID, args.CharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	characterData := &structpb.Struct{}
	if err := protojson.Unmarshal(character.CharacterData, characterData); err != nil {
		return fmt.Sprintf("parsing stored character data: %v", err), false, "internal_error"
	}

	listResp, err := s.systemEngine.ListInventory(ctx, &systemenginepb.ListInventoryRequest{
		RequestId: "dm-tool-" + character.ID,
		Actor:     &systemenginepb.Actor{ActorId: character.ID, CharacterData: characterData, SchemaVersion: character.SchemaVersion},
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !listResp.Success {
		return listResp.Error, false, "list_inventory_failed"
	}

	items := make([]map[string]any, 0, len(listResp.ItemNames))
	for _, itemName := range listResp.ItemNames {
		priceCopper, ok, reason, err := s.itemPriceCopper(ctx, campaignID, itemName)
		if err != nil {
			return err.Error(), false, "engine_error"
		}
		if !ok {
			// A real member of this character's inventory that GetItemInfo
			// doesn't recognize would mean the item library disagrees with
			// itself between AddItemToInventory and GetItemInfo — an engine
			// inconsistency, not a normal player-facing rejection.
			return fmt.Sprintf("inventory item %q has no price info: %s", itemName, reason), false, "internal_error"
		}
		copper, silver, gold, platinum := copperToDenominations(priceCopper)
		items = append(items, map[string]any{
			"item_name": itemName,
			"copper":    copper,
			"silver":    silver,
			"gold":      gold,
			"platinum":  platinum,
		})
	}

	payload, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmVendorSellItem moves a real item from vendorCharacterID's inventory
// to buyerCharacterID's, and the item's real price (itemPriceCopper) from
// buyer to vendor — a purchase. Both legs are real system-engine calls
// (TransferItem, then TransferCurrency chained on top of TransferItem's
// own returned state, so the final persisted character_data reflects
// both changes together); Master never persists either character via
// SaveCharacter until BOTH legs have succeeded, so a second-leg failure
// (the buyer can't afford it) needs no compensating reverse call — the
// store still holds the original, untouched state for both characters,
// exactly as if the tool had never been called.
func (s *Server) dmVendorSellItem(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		VendorCharacterID string `json:"vendor_character_id"`
		BuyerCharacterID  string `json:"buyer_character_id"`
		ItemName          string `json:"item_name"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	vendor, err := s.campaignCharacter(ctx, campaignID, args.VendorCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	buyer, err := s.campaignCharacter(ctx, campaignID, args.BuyerCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}

	if reason, code, err := s.pvpGateBlocked(ctx, campaignID, actingSenderID, "an item", vendor); err != nil {
		return fmt.Sprintf("checking character status: %v", err), false, "engine_error"
	} else if reason != "" {
		return reason, false, code
	}

	priceCopper, ok, reason, err := s.itemPriceCopper(ctx, campaignID, args.ItemName)
	if err != nil {
		return err.Error(), false, "engine_error"
	}
	if !ok {
		return reason, false, "item_not_found"
	}
	priceCopperArg, priceSilverArg, priceGoldArg, pricePlatinumArg := copperToDenominations(priceCopper)

	vendorData := &structpb.Struct{}
	if err := protojson.Unmarshal(vendor.CharacterData, vendorData); err != nil {
		return fmt.Sprintf("parsing stored vendor data: %v", err), false, "internal_error"
	}
	buyerData := &structpb.Struct{}
	if err := protojson.Unmarshal(buyer.CharacterData, buyerData); err != nil {
		return fmt.Sprintf("parsing stored buyer data: %v", err), false, "internal_error"
	}

	itemResp, err := s.systemEngine.TransferItem(ctx, &systemenginepb.TransferItemRequest{
		RequestId:  "dm-tool-vendor-sell-" + vendor.ID,
		CampaignId: campaignID,
		Source:     &systemenginepb.Actor{ActorId: vendor.ID, CharacterData: vendorData, SchemaVersion: vendor.SchemaVersion},
		Target:     &systemenginepb.Actor{ActorId: buyer.ID, CharacterData: buyerData, SchemaVersion: buyer.SchemaVersion},
		ItemName:   args.ItemName,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !itemResp.Success {
		return itemResp.Error, false, "vendor_sell_failed"
	}

	currencyResp, err := s.systemEngine.TransferCurrency(ctx, &systemenginepb.TransferCurrencyRequest{
		RequestId:  "dm-tool-vendor-sell-currency-" + buyer.ID,
		CampaignId: campaignID,
		Source:     itemResp.Target, // buyer, updated with the item they just received
		Target:     itemResp.Source, // vendor, updated with the item removed
		Copper:     priceCopperArg,
		Silver:     priceSilverArg,
		Gold:       priceGoldArg,
		Platinum:   pricePlatinumArg,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !currencyResp.Success {
		// Nothing has been persisted yet (SaveCharacter hasn't been called
		// for either character) — the store still holds the original,
		// pre-transaction state for both, so no compensating call is
		// needed. The buyer simply can't afford it.
		return currencyResp.Error, false, "insufficient_funds"
	}

	if err := s.saveUpdatedCharacter(ctx, buyer, currencyResp.Source); err != nil {
		return err.Error(), false, "internal_error"
	}
	if err := s.saveUpdatedCharacter(ctx, vendor, currencyResp.Target); err != nil {
		return err.Error(), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{
		"sold":     true,
		"item":     args.ItemName,
		"copper":   priceCopperArg,
		"silver":   priceSilverArg,
		"gold":     priceGoldArg,
		"platinum": pricePlatinumArg,
		"message":  fmt.Sprintf("%s buys %s from %s.", buyer.ID, args.ItemName, vendor.ID),
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// dmVendorBuyItem is dmVendorSellItem with the item and currency legs
// reversed: a real item moves from sellerCharacterID to
// vendorCharacterID, and the item's real price moves from vendor to
// seller — vendorCharacterID's own currency is a real, finite
// constraint here, same as any character's. Same no-compensating-call
// reasoning as dmVendorSellItem: nothing is persisted until both engine
// legs succeed.
func (s *Server) dmVendorBuyItem(ctx context.Context, campaignID, actingSenderID string, argsJSON json.RawMessage) (string, bool, string) {
	var args struct {
		VendorCharacterID string `json:"vendor_character_id"`
		SellerCharacterID string `json:"seller_character_id"`
		ItemName          string `json:"item_name"`
	}
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return fmt.Sprintf("invalid arguments: %v", err), false, "invalid_arguments"
	}

	vendor, err := s.campaignCharacter(ctx, campaignID, args.VendorCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}
	seller, err := s.campaignCharacter(ctx, campaignID, args.SellerCharacterID)
	if err != nil {
		return err.Error(), false, "character_not_found"
	}

	if reason, code, err := s.pvpGateBlocked(ctx, campaignID, actingSenderID, "an item", seller); err != nil {
		return fmt.Sprintf("checking character status: %v", err), false, "engine_error"
	} else if reason != "" {
		return reason, false, code
	}

	priceCopper, ok, reason, err := s.itemPriceCopper(ctx, campaignID, args.ItemName)
	if err != nil {
		return err.Error(), false, "engine_error"
	}
	if !ok {
		return reason, false, "item_not_found"
	}
	priceCopperArg, priceSilverArg, priceGoldArg, pricePlatinumArg := copperToDenominations(priceCopper)

	sellerData := &structpb.Struct{}
	if err := protojson.Unmarshal(seller.CharacterData, sellerData); err != nil {
		return fmt.Sprintf("parsing stored seller data: %v", err), false, "internal_error"
	}
	vendorData := &structpb.Struct{}
	if err := protojson.Unmarshal(vendor.CharacterData, vendorData); err != nil {
		return fmt.Sprintf("parsing stored vendor data: %v", err), false, "internal_error"
	}

	itemResp, err := s.systemEngine.TransferItem(ctx, &systemenginepb.TransferItemRequest{
		RequestId:  "dm-tool-vendor-buy-" + seller.ID,
		CampaignId: campaignID,
		Source:     &systemenginepb.Actor{ActorId: seller.ID, CharacterData: sellerData, SchemaVersion: seller.SchemaVersion},
		Target:     &systemenginepb.Actor{ActorId: vendor.ID, CharacterData: vendorData, SchemaVersion: vendor.SchemaVersion},
		ItemName:   args.ItemName,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !itemResp.Success {
		return itemResp.Error, false, "vendor_buy_failed"
	}

	currencyResp, err := s.systemEngine.TransferCurrency(ctx, &systemenginepb.TransferCurrencyRequest{
		RequestId:  "dm-tool-vendor-buy-currency-" + vendor.ID,
		CampaignId: campaignID,
		Source:     itemResp.Target, // vendor, updated with the item they just received
		Target:     itemResp.Source, // seller, updated with the item removed
		Copper:     priceCopperArg,
		Silver:     priceSilverArg,
		Gold:       priceGoldArg,
		Platinum:   pricePlatinumArg,
	})
	if err != nil {
		return fmt.Sprintf("calling system engine: %v", err), false, "engine_error"
	}
	if !currencyResp.Success {
		// Nothing has been persisted yet — the store still holds the
		// original, pre-transaction state for both characters. The
		// vendor's own cash reserves simply aren't enough.
		return currencyResp.Error, false, "vendor_insufficient_funds"
	}

	if err := s.saveUpdatedCharacter(ctx, vendor, currencyResp.Source); err != nil {
		return err.Error(), false, "internal_error"
	}
	if err := s.saveUpdatedCharacter(ctx, seller, currencyResp.Target); err != nil {
		return err.Error(), false, "internal_error"
	}

	payload, err := json.Marshal(map[string]any{
		"bought":   true,
		"item":     args.ItemName,
		"copper":   priceCopperArg,
		"silver":   priceSilverArg,
		"gold":     priceGoldArg,
		"platinum": pricePlatinumArg,
		"message":  fmt.Sprintf("%s buys %s from %s.", vendor.ID, args.ItemName, seller.ID),
	})
	if err != nil {
		return fmt.Sprintf("marshaling result: %v", err), false, "internal_error"
	}
	return string(payload), true, ""
}

// saveUpdatedCharacter marshals updatedData (a fresh Actor.character_data
// returned by a system-engine call) back onto character and persists it
// — the same marshal-then-SaveCharacter pattern every other DM tool in
// this package already repeats per character; factored out here only
// because dmVendorSellItem/dmVendorBuyItem each do it twice per call.
func (s *Server) saveUpdatedCharacter(ctx context.Context, character store.Character, updatedActor *systemenginepb.Actor) error {
	newData, err := protojson.Marshal(updatedActor.CharacterData)
	if err != nil {
		return fmt.Errorf("marshaling updated character data: %w", err)
	}
	character.CharacterData = newData
	character.UpdatedAt = time.Now().UTC()
	if err := s.characters.SaveCharacter(ctx, character); err != nil {
		return fmt.Errorf("saving updated character: %w", err)
	}
	return nil
}
