/**
 * Copyright 2023 Gravitational, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { NavLink } from 'react-router-dom';
import styled, { css } from 'styled-components';

export interface OpenProps {
  open: boolean;
}

export const STARTING_TRANSITION_DELAY = 80;
export const INCREMENT_TRANSITION_DELAY = 20;

export const Dropdown = styled.div<OpenProps>`
  position: absolute;
  display: flex;
  flex-direction: column;
  padding: ${p => p.theme.space[2]}px ${p => p.theme.space[3]}px;
  background: ${({ theme }) => theme.colors.levels.elevated};
  box-shadow: ${({ theme }) => theme.boxShadow[1]};
  border-radius: ${p => p.theme.radii[2]}px;
  width: 265px;
  right: 0;
  top: 43px;
  z-index: 999;
  opacity: ${p => (p.open ? 1 : 0)};
  visibility: ${p => (p.open ? 'visible' : 'hidden')};
  transform-origin: top right;
  transition: opacity 0.2s ease, visibility 0.2s ease,
    transform 0.3s cubic-bezier(0.45, 0.6, 0.5, 1.25);
  transform: ${p =>
    p.open ? 'scale(1) translate(0, 12px)' : 'scale(.8) translate(0, 4px)'};
`;

export const DropdownItem = styled.div`
  line-height: 1;
  font-size: ${p => p.theme.fontSizes[2]}px;
  color: ${props => props.theme.colors.text.main};
  cursor: pointer;
  border-radius: ${p => p.theme.radii[2]}px;
  margin-bottom: ${p => p.theme.space[1]}px;
  opacity: ${p => (p.open ? 1 : 0)};
  transition: transform 0.3s ease, opacity 0.7s ease;
  transform: translate3d(${p => (p.open ? 0 : '20px')}, 0, 0);
  transition-delay: ${p => p.$transitionDelay}ms;

  &:hover {
    background: ${props => props.theme.colors.spotBackground[0]};
  }

  &:last-of-type {
    margin-bottom: 0;
  }
`;

export const commonDropdownItemStyles = css`
  align-items: center;
  display: flex;
  padding: ${p => p.theme.space[1] * 3}px;
  color: ${props => props.theme.colors.text.main};
  text-decoration: none;

  svg {
    height: 18px;
    width: 18px;
  }
`;

export const DropdownItemButton = styled.div`
  ${commonDropdownItemStyles};
`;

export const DropdownItemLink = styled(NavLink)`
  ${commonDropdownItemStyles};
`;

export const DropdownItemIcon = styled.div`
  margin-right: ${p => p.theme.space[3]}px;
  line-height: 0;
`;

export const DropdownDivider = styled.div`
  height: 1px;
  background: ${props => props.theme.colors.spotBackground[1]};
  margin: ${props => props.theme.space[1]}px;
  margin-top: 0;
`;
